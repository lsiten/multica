package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// documentContentVersionOwnerType is the only kind this handler records.
// Attachments (binary) version through a separate, attachment-scoped path;
// they are not accepted here so a text edit cannot silently create a binary
// version row.
const documentContentVersionOwnerType = "issue"

// DocumentContentVersionResponse is the JSON shape of one committed content
// version. content and content_url are mutually exclusive: text versions carry
// content, binary versions carry content_url.
type DocumentContentVersionResponse struct {
	ID            string  `json:"id"`
	OwnerType     string  `json:"owner_type"`
	OwnerID       string  `json:"owner_id"`
	Revision      int64   `json:"revision"`
	OwnerRevision *int64  `json:"owner_revision,omitempty"`
	ContentType   string  `json:"content_type"`
	Content       *string `json:"content,omitempty"`
	ContentURL    *string `json:"content_url,omitempty"`
	SizeBytes     int64   `json:"size_bytes"`
	ActorType     string  `json:"actor_type"`
	ActorID       string  `json:"actor_id"`
	Merged        bool    `json:"merged"`
	SessionID     *string `json:"session_id,omitempty"`
	Note          *string `json:"note,omitempty"`
	CreatedAt     string  `json:"created_at"`
}

func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	s := t.String
	return &s
}

func documentContentVersionToResponse(v db.DocumentContentVersion) DocumentContentVersionResponse {
	return DocumentContentVersionResponse{
		ID:          uuidToString(v.ID),
		OwnerType:   v.OwnerType,
		OwnerID:     uuidToString(v.OwnerID),
		Revision:    v.Revision,
		ContentType: v.ContentType,
		Content:     textPtr(v.Content),
		ContentURL:  textPtr(v.ContentUrl),
		SizeBytes:   v.SizeBytes,
		ActorType:   v.ActorType,
		ActorID:     uuidToString(v.ActorID),
		Merged:      v.Merged,
		SessionID:   textPtr(v.SessionID),
		Note:        textPtr(v.Note),
		CreatedAt:   timestampToString(v.CreatedAt),
	}
}

// RecordDocumentContentVersionRequest is the body of a content-version commit.
// Exactly one of Content or ContentURL must be set. ExpectedRevision is an
// optional compare-and-swap guard against the issue's current revision. When
// Merge is true, a consecutive edit by the SAME actor folds into that actor's
// latest version instead of appending a new revision (D4: keep an agent's
// rapid multi-edit sequence out of the log).
type RecordDocumentContentVersionRequest struct {
	ContentType      string  `json:"content_type"`
	Content          *string `json:"content,omitempty"`
	ContentURL       *string `json:"content_url,omitempty"`
	SizeBytes        int64   `json:"size_bytes"`
	Note             *string `json:"note,omitempty"`
	ExpectedRevision *int64  `json:"expected_revision,omitempty"`
	Merge            *bool   `json:"merge,omitempty"`
	SessionID        *string `json:"session_id,omitempty"`
}

// sameActor compares two actor UUIDs treating an invalid stored UUID as a
// non-match rather than a panic.
func sameActor(a, b pgtype.UUID) bool {
	return a.Valid && b.Valid && a.Bytes == b.Bytes
}

// RecordDocumentContentVersion commits a new content version for an issue's
// Markdown body (POST /api/issues/{id}/content-versions).
func (h *Handler) RecordDocumentContentVersion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}
	var req RecordDocumentContentVersionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.recordContentVersionForIssue(w, r, issue, req)
}

// ListDocumentContentVersions returns an issue's content version history,
// newest first (GET /api/issues/{id}/content-versions).
func (h *Handler) ListDocumentContentVersions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}
	rows, err := h.Queries.ListDocumentContentVersions(r.Context(), db.ListDocumentContentVersionsParams{
		OwnerType:   documentContentVersionOwnerType,
		OwnerID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list content versions")
		return
	}
	out := make([]DocumentContentVersionResponse, len(rows))
	for i, row := range rows {
		out[i] = documentContentVersionToResponse(row)
	}
	writeJSON(w, http.StatusOK, out)
}

// GetDocumentContentVersion returns a single content version
// (GET /api/issues/{id}/content-versions/{revision}).
func (h *Handler) GetDocumentContentVersion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}
	revision, ok := parsePositiveInt64Param(chi.URLParam(r, "revision"))
	if !ok {
		writeError(w, http.StatusBadRequest, "revision must be a positive integer")
		return
	}
	row, err := h.Queries.GetDocumentContentVersion(r.Context(), db.GetDocumentContentVersionParams{
		OwnerType:   documentContentVersionOwnerType,
		OwnerID:     issue.ID,
		Revision:    revision,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "content version not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load content version")
		return
	}
	writeJSON(w, http.StatusOK, documentContentVersionToResponse(row))
}

// DeleteDocumentContentVersion removes an older named checkpoint. Autosaves and
// the current version remain part of the linear history.
func (h *Handler) DeleteDocumentContentVersion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}
	revision, ok := parsePositiveInt64Param(chi.URLParam(r, "revision"))
	if !ok {
		writeError(w, http.StatusBadRequest, "revision must be a positive integer")
		return
	}
	target, err := h.Queries.GetDocumentContentVersion(r.Context(), db.GetDocumentContentVersionParams{
		OwnerType:   documentContentVersionOwnerType,
		OwnerID:     issue.ID,
		Revision:    revision,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "content version not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load content version")
		return
	}
	latest, err := h.Queries.GetLatestDocumentContentVersion(r.Context(), db.GetLatestDocumentContentVersionParams{
		OwnerType:   documentContentVersionOwnerType,
		OwnerID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load current content version")
		return
	}
	if target.Merged || target.Revision == latest.Revision {
		writeError(w, http.StatusConflict, "only older named checkpoints can be deleted")
		return
	}
	deleted, err := h.Queries.DeleteDocumentContentVersion(r.Context(), db.DeleteDocumentContentVersionParams{
		OwnerType:   documentContentVersionOwnerType,
		OwnerID:     issue.ID,
		Revision:    revision,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete content version")
		return
	}
	if deleted == 0 {
		writeError(w, http.StatusConflict, "only older named checkpoints can be deleted")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RollbackDocumentContentVersion restores an issue's Markdown body to a prior
// content version, recording the restoration as a new version so history stays
// linear (POST /api/issues/{id}/content-versions/{revision}/rollback).
func (h *Handler) RollbackDocumentContentVersion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}
	revision, ok := parsePositiveInt64Param(chi.URLParam(r, "revision"))
	if !ok {
		writeError(w, http.StatusBadRequest, "revision must be a positive integer")
		return
	}
	target, err := h.Queries.GetDocumentContentVersion(r.Context(), db.GetDocumentContentVersionParams{
		OwnerType:   documentContentVersionOwnerType,
		OwnerID:     issue.ID,
		Revision:    revision,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "content version not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load content version")
		return
	}
	if !target.Content.Valid {
		// Only text versions can be rolled back onto an issue body; binary
		// versions version through the attachment path (follow-up).
		writeError(w, http.StatusNotImplemented, "binary document rollback is not supported on an issue body")
		return
	}

	var req RecordDocumentContentVersionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// A rollback adopts the target version's content verbatim and is always a
	// new revision (never merged), so the restored state is its own history row.
	req.Content = &target.Content.String
	req.ContentURL = nil
	if req.ContentType == "" {
		req.ContentType = target.ContentType
	}
	req.SizeBytes = target.SizeBytes
	req.Merge = nil
	if req.Note == nil {
		note := "rollback to revision " + strconv.FormatInt(revision, 10)
		req.Note = &note
	}
	h.recordContentVersionForIssue(w, r, issue, req)
}

// documentContentVersionAttachmentType is the owner kind for binary documents
// (an image or a PDF attachment). Unlike an issue's Markdown body, a binary's
// bytes are never rewritten in place: a change produces a new blob and a new
// version row, while the prior row is kept for rollback.
const documentContentVersionAttachmentType = "attachment"

// recordContentVersionForAttachment records (or merges into) a content version
// for an attachment's binary blob. The caller has already uploaded the new
// blob; ContentURL is its reference. There is no in-place apply, so this is a
// pure version-log append (or a D4 merge), guarded only by the unique
// (owner_type, owner_id, revision) index.
func (h *Handler) recordContentVersionForAttachment(w http.ResponseWriter, r *http.Request, att db.Attachment, req RecordDocumentContentVersionRequest) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if req.ContentType == "" {
		writeError(w, http.StatusBadRequest, "content_type is required")
		return
	}
	if req.SessionID != nil && (strings.TrimSpace(*req.SessionID) == "" || len(*req.SessionID) > 128) {
		writeError(w, http.StatusBadRequest, "session_id is invalid")
		return
	}
	// Binary versions carry a blob reference, never inline text.
	if req.ContentURL == nil {
		writeError(w, http.StatusBadRequest, "content_url is required for a binary content version")
		return
	}
	if req.Content != nil {
		writeError(w, http.StatusBadRequest, "exactly one of content or content_url is required")
		return
	}

	actorType, actorID := h.resolveActor(r, userID, uuidToString(att.WorkspaceID))
	actorUUID, err := util.ParseUUID(actorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid actor")
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
	sizeBytes := req.SizeBytes
	if sizeBytes == 0 {
		// Fall back to the attachment's known size when the caller omits it.
		sizeBytes = att.SizeBytes
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin content version transaction")
		return
	}
	defer tx.Rollback(r.Context())
	q := h.Queries.WithTx(tx)

	// Merge (D4): fold a consecutive same-actor edit into its latest version.
	if req.Merge != nil && *req.Merge {
		if latest, lerr := q.GetLatestDocumentContentVersion(r.Context(), db.GetLatestDocumentContentVersionParams{
			OwnerType:   documentContentVersionAttachmentType,
			OwnerID:     att.ID,
			WorkspaceID: att.WorkspaceID,
		}); lerr == nil && latest.ActorType == actorType && sameActor(latest.ActorID, actorUUID) && (!latest.Note.Valid || strings.TrimSpace(latest.Note.String) == "") {
			merged, uerr := q.UpdateDocumentContentVersionContent(r.Context(), db.UpdateDocumentContentVersionContentParams{
				Content:     pgtype.Text{},
				ContentUrl:  pgtype.Text{String: *req.ContentURL, Valid: true},
				SizeBytes:   sizeBytes,
				ActorType:   actorType,
				ActorID:     actorUUID,
				Note:        note,
				Merged:      true,
				SessionID:   sessionID,
				OwnerType:   documentContentVersionAttachmentType,
				OwnerID:     att.ID,
				Revision:    latest.Revision,
				WorkspaceID: att.WorkspaceID,
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
			writeJSON(w, http.StatusOK, documentContentVersionToResponse(merged))
			return
		}
	}

	rev, err := h.nextContentRevision(q, r.Context(), documentContentVersionAttachmentType, att.ID, att.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute next content version")
		return
	}
	row, err := q.InsertDocumentContentVersion(r.Context(), db.InsertDocumentContentVersionParams{
		WorkspaceID: att.WorkspaceID,
		OwnerType:   documentContentVersionAttachmentType,
		OwnerID:     att.ID,
		Revision:    rev,
		ContentType: req.ContentType,
		Content:     pgtype.Text{},
		ContentUrl:  pgtype.Text{String: *req.ContentURL, Valid: true},
		SizeBytes:   sizeBytes,
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
	h.publishDocumentContentVersion(row)
	writeJSON(w, http.StatusCreated, documentContentVersionToResponse(row))
}

// loadAttachmentForContentVersion loads the attachment behind an
// /api/attachments/{id}/... content-version route.
func (h *Handler) loadAttachmentForContentVersion(w http.ResponseWriter, r *http.Request) (db.Attachment, bool) {
	return h.loadAttachmentForRequest(w, r)
}

// RecordDocumentContentVersionForAttachment commits a new content version for an
// attachment's binary blob (POST /api/attachments/{id}/content-versions).
func (h *Handler) RecordDocumentContentVersionForAttachment(w http.ResponseWriter, r *http.Request) {
	att, ok := h.loadAttachmentForContentVersion(w, r)
	if !ok {
		return
	}
	var req RecordDocumentContentVersionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.recordContentVersionForAttachment(w, r, att, req)
}

// ListDocumentContentVersionsForAttachment returns an attachment's content
// version history, newest first (GET /api/attachments/{id}/content-versions).
func (h *Handler) ListDocumentContentVersionsForAttachment(w http.ResponseWriter, r *http.Request) {
	att, ok := h.loadAttachmentForContentVersion(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListDocumentContentVersions(r.Context(), db.ListDocumentContentVersionsParams{
		OwnerType:   documentContentVersionAttachmentType,
		OwnerID:     att.ID,
		WorkspaceID: att.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list content versions")
		return
	}
	out := make([]DocumentContentVersionResponse, len(rows))
	for i, row := range rows {
		out[i] = documentContentVersionToResponse(row)
	}
	writeJSON(w, http.StatusOK, out)
}

// GetDocumentContentVersionForAttachment returns a single content version
// (GET /api/attachments/{id}/content-versions/{revision}).
func (h *Handler) GetDocumentContentVersionForAttachment(w http.ResponseWriter, r *http.Request) {
	att, ok := h.loadAttachmentForContentVersion(w, r)
	if !ok {
		return
	}
	revision, ok := parsePositiveInt64Param(chi.URLParam(r, "revision"))
	if !ok {
		writeError(w, http.StatusBadRequest, "revision must be a positive integer")
		return
	}
	row, err := h.Queries.GetDocumentContentVersion(r.Context(), db.GetDocumentContentVersionParams{
		OwnerType:   documentContentVersionAttachmentType,
		OwnerID:     att.ID,
		Revision:    revision,
		WorkspaceID: att.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "content version not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load content version")
		return
	}
	writeJSON(w, http.StatusOK, documentContentVersionToResponse(row))
}

// DeleteDocumentContentVersionForAttachment removes an older named checkpoint
// from a binary document while preserving autosaves and the current version.
func (h *Handler) DeleteDocumentContentVersionForAttachment(w http.ResponseWriter, r *http.Request) {
	att, ok := h.loadAttachmentForContentVersion(w, r)
	if !ok {
		return
	}
	revision, ok := parsePositiveInt64Param(chi.URLParam(r, "revision"))
	if !ok {
		writeError(w, http.StatusBadRequest, "revision must be a positive integer")
		return
	}
	target, err := h.Queries.GetDocumentContentVersion(r.Context(), db.GetDocumentContentVersionParams{
		OwnerType:   documentContentVersionAttachmentType,
		OwnerID:     att.ID,
		Revision:    revision,
		WorkspaceID: att.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "content version not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load content version")
		return
	}
	latest, err := h.Queries.GetLatestDocumentContentVersion(r.Context(), db.GetLatestDocumentContentVersionParams{
		OwnerType:   documentContentVersionAttachmentType,
		OwnerID:     att.ID,
		WorkspaceID: att.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load current content version")
		return
	}
	if target.Merged || target.Revision == latest.Revision {
		writeError(w, http.StatusConflict, "only older named checkpoints can be deleted")
		return
	}
	deleted, err := h.Queries.DeleteDocumentContentVersion(r.Context(), db.DeleteDocumentContentVersionParams{
		OwnerType:   documentContentVersionAttachmentType,
		OwnerID:     att.ID,
		Revision:    revision,
		WorkspaceID: att.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete content version")
		return
	}
	if deleted == 0 {
		writeError(w, http.StatusConflict, "only older named checkpoints can be deleted")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RollbackDocumentContentVersionForAttachment restores an attachment's binary
// blob to a prior content version, recording the restoration as a new version
// so history stays linear (POST /api/attachments/{id}/content-versions/
// {revision}/rollback). A binary is never rewritten in place, so this appends a
// new version row whose content_url points back at the target version's blob.
func (h *Handler) RollbackDocumentContentVersionForAttachment(w http.ResponseWriter, r *http.Request) {
	att, ok := h.loadAttachmentForContentVersion(w, r)
	if !ok {
		return
	}
	revision, ok := parsePositiveInt64Param(chi.URLParam(r, "revision"))
	if !ok {
		writeError(w, http.StatusBadRequest, "revision must be a positive integer")
		return
	}
	target, err := h.Queries.GetDocumentContentVersion(r.Context(), db.GetDocumentContentVersionParams{
		OwnerType:   documentContentVersionAttachmentType,
		OwnerID:     att.ID,
		Revision:    revision,
		WorkspaceID: att.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "content version not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load content version")
		return
	}

	var req RecordDocumentContentVersionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// A rollback adopts the target version's blob verbatim and is always a new
	// revision (never merged), so the restored state is its own history row.
	req.Content = nil
	req.ContentURL = &target.ContentUrl.String
	if req.ContentType == "" {
		req.ContentType = target.ContentType
	}
	req.SizeBytes = target.SizeBytes
	req.Merge = nil
	if req.Note == nil {
		note := "rollback to revision " + strconv.FormatInt(revision, 10)
		req.Note = &note
	}
	h.recordContentVersionForAttachment(w, r, att, req)
}

// GetDocumentContentVersionLatest returns the most recent (current) content
// version for an issue's document (GET /api/issues/{id}/content-versions/
// current). It is the read-side foundation the agent's read_document tool calls:
// the latest version's content is the current Markdown body the agent should
// read and edit against. Returns 404 when the document has no version history.
func (h *Handler) GetDocumentContentVersionLatest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}
	h.writeLatestContentVersion(w, r, documentContentVersionOwnerType, issue.ID, issue.WorkspaceID, &issue)
}

// GetDocumentContentVersionLatestForAttachment returns the most recent (current)
// content version for an attachment's binary document (GET
// /api/attachments/{id}/content-versions/current). The latest version's
// content_url is the current blob the agent should read; a document with no
// version history returns 404 (the agent then falls back to the attachment's own
// url).
func (h *Handler) GetDocumentContentVersionLatestForAttachment(w http.ResponseWriter, r *http.Request) {
	att, ok := h.loadAttachmentForContentVersion(w, r)
	if !ok {
		return
	}
	h.writeLatestContentVersion(w, r, documentContentVersionAttachmentType, att.ID, att.WorkspaceID, nil)
}

// writeLatestContentVersion serves the current content version for one owner,
// shared by the issue and attachment read paths.
func (h *Handler) writeLatestContentVersion(w http.ResponseWriter, r *http.Request, ownerType string, ownerID, workspaceID pgtype.UUID, issue *db.Issue) {
	row, err := h.Queries.GetLatestDocumentContentVersion(r.Context(), db.GetLatestDocumentContentVersionParams{
		OwnerType:   ownerType,
		OwnerID:     ownerID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "content version not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load content version")
		return
	}
	response := documentContentVersionToResponse(row)
	if issue != nil {
		response.OwnerRevision = &issue.Revision
		response.Content = textPtr(issue.Description)
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) nextContentRevision(q *db.Queries, ctx context.Context, ownerType string, ownerID, workspaceID pgtype.UUID) (int64, error) {
	latest, err := q.GetLatestDocumentContentVersion(ctx, db.GetLatestDocumentContentVersionParams{
		OwnerType:   ownerType,
		OwnerID:     ownerID,
		WorkspaceID: workspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	return latest.Revision + 1, nil
}

// parsePositiveInt64Param parses a URL path parameter as a positive int64.
func parsePositiveInt64Param(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}
