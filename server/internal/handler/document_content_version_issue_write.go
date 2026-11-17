package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// recordContentVersionForIssue applies content to an issue's Markdown body and
// appends (or merges into) a version row. The whole apply+record is one
// transaction so the issue body and its version log can never diverge.
func (h *Handler) recordContentVersionForIssue(w http.ResponseWriter, r *http.Request, issue db.Issue, req RecordDocumentContentVersionRequest) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}

	hasText := req.Content != nil
	hasURL := req.ContentURL != nil
	if req.ContentType == "" {
		writeError(w, http.StatusBadRequest, "content_type is required")
		return
	}
	if req.SessionID != nil && (strings.TrimSpace(*req.SessionID) == "" || len(*req.SessionID) > 128) {
		writeError(w, http.StatusBadRequest, "session_id is invalid")
		return
	}
	if hasText == hasURL {
		writeError(w, http.StatusBadRequest, "exactly one of content or content_url is required")
		return
	}

	// Fast-fail CAS before touching anything; the apply below re-checks it under
	// the row lock so this is only an optimization, not the guard.
	if req.ExpectedRevision != nil {
		if *req.ExpectedRevision < 1 {
			writeError(w, http.StatusBadRequest, "expected_revision must be a positive integer")
			return
		}
		if issue.Revision != *req.ExpectedRevision {
			writeRevisionConflict(w, "issue", issue.ID, *req.ExpectedRevision, issue.Revision)
			return
		}
	}

	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	actorUUID, err := util.ParseUUID(actorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid actor")
		return
	}

	// A binary version cannot be applied to an issue's Markdown body in this
	// path; binary documents version through the attachment path (follow-up).
	if !hasText {
		writeError(w, http.StatusNotImplemented, "binary document content versioning is not supported on an issue body")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin content version transaction")
		return
	}
	defer tx.Rollback(r.Context())
	q := h.Queries.WithTx(tx)

	lockedIssue, err := q.LockIssueForDescriptionUpdate(r.Context(), db.LockIssueForDescriptionUpdateParams{
		ID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock document")
		return
	}
	baselineCreated, err := preserveInitialDocumentVersion(r.Context(), q, lockedIssue, *req.Content, actorType, actorUUID, req.SessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to preserve original document")
		return
	}

	expected := pgtype.Int8{}
	if req.ExpectedRevision != nil {
		expected = pgtype.Int8{Int64: *req.ExpectedRevision, Valid: true}
	}
	if _, err = q.ApplyIssueDescriptionForContentVersion(r.Context(), db.ApplyIssueDescriptionForContentVersionParams{
		Description:      pgtype.Text{String: *req.Content, Valid: true},
		ID:               issue.ID,
		WorkspaceID:      issue.WorkspaceID,
		ExpectedRevision: expected,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The row guard lost the race: report the conflict against the
			// freshest revision so the client can reconcile.
			current, gerr := q.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
				ID:          issue.ID,
				WorkspaceID: issue.WorkspaceID,
			})
			if gerr == nil {
				writeRevisionConflict(w, "issue", issue.ID, issue.Revision, current.Revision)
				return
			}
		}
		writeError(w, http.StatusInternalServerError, "failed to apply content version")
		return
	}

	var note pgtype.Text
	if req.Note != nil {
		note = pgtype.Text{String: *req.Note, Valid: true}
	}
	var sessionID pgtype.Text
	if req.SessionID != nil {
		sessionID = pgtype.Text{String: *req.SessionID, Valid: true}
	}
	// Merge (D4): fold a consecutive same-actor edit into its latest version
	// instead of appending, so a burst of edits stays one revision.
	if req.Merge != nil && *req.Merge && !baselineCreated {
		if latest, lerr := q.GetLatestDocumentContentVersion(r.Context(), db.GetLatestDocumentContentVersionParams{
			OwnerType:   documentContentVersionOwnerType,
			OwnerID:     issue.ID,
			WorkspaceID: issue.WorkspaceID,
		}); lerr == nil && latest.ActorType == actorType && sameActor(latest.ActorID, actorUUID) && (!latest.Note.Valid || strings.TrimSpace(latest.Note.String) == "") {
			merged, uerr := q.UpdateDocumentContentVersionContent(r.Context(), db.UpdateDocumentContentVersionContentParams{
				Content:     pgtype.Text{String: *req.Content, Valid: true},
				ContentUrl:  pgtype.Text{},
				SizeBytes:   req.SizeBytes,
				ActorType:   actorType,
				ActorID:     actorUUID,
				Note:        note,
				Merged:      true,
				SessionID:   sessionID,
				OwnerType:   documentContentVersionOwnerType,
				OwnerID:     issue.ID,
				Revision:    latest.Revision,
				WorkspaceID: issue.WorkspaceID,
			})
			if uerr != nil {
				writeError(w, http.StatusInternalServerError, "failed to merge content version")
				return
			}
			if err = tx.Commit(r.Context()); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to commit content version")
				return
			}
			h.publishDocumentContentVersion(merged)
			response := documentContentVersionToResponse(merged)
			if current, gerr := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
				ID:          issue.ID,
				WorkspaceID: issue.WorkspaceID,
			}); gerr == nil {
				response.OwnerRevision = &current.Revision
			}
			writeJSON(w, http.StatusOK, response)
			return
		}
	}

	rev, err := h.nextContentRevision(q, r.Context(), documentContentVersionOwnerType, issue.ID, issue.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute next content version")
		return
	}
	row, err := q.InsertDocumentContentVersion(r.Context(), db.InsertDocumentContentVersionParams{
		WorkspaceID: issue.WorkspaceID,
		OwnerType:   documentContentVersionOwnerType,
		OwnerID:     issue.ID,
		Revision:    rev,
		ContentType: req.ContentType,
		Content:     pgtype.Text{String: *req.Content, Valid: true},
		ContentUrl:  pgtype.Text{},
		SizeBytes:   req.SizeBytes,
		ActorType:   actorType,
		ActorID:     actorUUID,
		Note:        note,
		Merged:      req.Merge != nil && *req.Merge,
		SessionID:   sessionID,
	})
	if err != nil {
		// A unique-index race means another writer took the next revision
		// number; surface it as a conflict so the caller retries.
		writeError(w, http.StatusConflict, "content version already recorded")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit content version")
		return
	}
	/* Requirement change flow: editing the document after confirmation reopens
	   the requirement so the agent can initiate a change confirmation. Best-effort.
	*/
	h.maybeReopenRequirementOnEdit(r.Context(), issue)
	h.publishDocumentContentVersion(row)
	response := documentContentVersionToResponse(row)
	if current, gerr := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
		ID:          issue.ID,
		WorkspaceID: issue.WorkspaceID,
	}); gerr == nil {
		response.OwnerRevision = &current.Revision
	}
	writeJSON(w, http.StatusCreated, response)
}

func preserveInitialDocumentVersion(ctx context.Context, q *db.Queries, issue db.Issue, nextContent, actorType string, actorID pgtype.UUID, sessionID *string) (bool, error) {
	if !issue.Description.Valid || issue.Description.String == "" || issue.Description.String == nextContent {
		return false, nil
	}
	_, err := q.GetLatestDocumentContentVersion(ctx, db.GetLatestDocumentContentVersionParams{
		OwnerType: documentContentVersionOwnerType, OwnerID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("read initial document history: %w", err)
	}
	var session pgtype.Text
	if sessionID != nil {
		session = pgtype.Text{String: *sessionID, Valid: true}
	}
	_, err = q.InsertDocumentContentVersion(ctx, db.InsertDocumentContentVersionParams{
		WorkspaceID: issue.WorkspaceID,
		OwnerType:   documentContentVersionOwnerType,
		OwnerID:     issue.ID,
		Revision:    1,
		ContentType: "text/markdown",
		Content:     issue.Description,
		SizeBytes:   int64(len(issue.Description.String)),
		ActorType:   actorType,
		ActorID:     actorID,
		SessionID:   session,
	})
	if err != nil {
		return false, fmt.Errorf("preserve initial document: %w", err)
	}
	return true, nil
}

func (h *Handler) recordIssueBodyChangeInTx(ctx context.Context, q *db.Queries, previous, current db.Issue, actorType, actorID string) (db.DocumentContentVersion, error) {
	actor, err := util.ParseUUID(actorID)
	if err != nil {
		return db.DocumentContentVersion{}, fmt.Errorf("invalid content-version actor: %w", err)
	}
	if _, err := preserveInitialDocumentVersion(ctx, q, previous, current.Description.String, actorType, actor, nil); err != nil {
		return db.DocumentContentVersion{}, err
	}
	revision, err := h.nextContentRevision(q, ctx, documentContentVersionOwnerType, current.ID, current.WorkspaceID)
	if err != nil {
		return db.DocumentContentVersion{}, err
	}
	return q.InsertDocumentContentVersion(ctx, db.InsertDocumentContentVersionParams{
		WorkspaceID: current.WorkspaceID, OwnerType: documentContentVersionOwnerType, OwnerID: current.ID,
		Revision: revision, ContentType: "text/markdown", Content: current.Description,
		SizeBytes: int64(len(current.Description.String)), ActorType: actorType, ActorID: actor,
	})
}
