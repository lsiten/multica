package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func lockDocumentCommentOwner(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, resource DocumentCommentResource) error {
	if _, err := q.LockWorkspaceForChatSessionCreate(ctx, workspaceID); err != nil {
		return err
	}
	var err error
	switch resource.Kind {
	case "issue":
		_, err = q.LockDocumentCommentIssue(ctx, db.LockDocumentCommentIssueParams{ID: parseUUID(resource.ID), WorkspaceID: workspaceID})
	case "attachment":
		attachment, readErr := q.GetAttachment(ctx, db.GetAttachmentParams{ID: parseUUID(resource.ID), WorkspaceID: workspaceID})
		if readErr != nil {
			return readErr
		}
		if attachment.SourceContextID.Valid {
			source, sourceErr := q.GetIssueSourceContextByID(ctx, db.GetIssueSourceContextByIDParams{ID: attachment.SourceContextID, WorkspaceID: workspaceID})
			if sourceErr != nil {
				return sourceErr
			}
			if source.IssueID.Valid {
				if _, err := q.LockDocumentCommentIssue(ctx, db.LockDocumentCommentIssueParams{ID: source.IssueID, WorkspaceID: workspaceID}); err != nil {
					return err
				}
			}
		}
		if attachment.IssueID.Valid {
			if _, err := q.LockDocumentCommentIssue(ctx, db.LockDocumentCommentIssueParams{ID: attachment.IssueID, WorkspaceID: workspaceID}); err != nil {
				return err
			}
		}
		if attachment.CommentID.Valid {
			if _, err := q.LockLiveComment(ctx, db.LockLiveCommentParams{ID: attachment.CommentID, WorkspaceID: workspaceID}); err != nil {
				return err
			}
		}
		locked, lockErr := q.LockAttachmentRow(ctx, db.LockAttachmentRowParams{ID: attachment.ID, WorkspaceID: workspaceID})
		if lockErr != nil {
			return lockErr
		}
		if locked.IssueID != attachment.IssueID || locked.CommentID != attachment.CommentID || locked.SourceContextID != attachment.SourceContextID {
			return errAttachmentOwnerChanged
		}
	case "workspace_file":
		if resource.ProjectResourceID != "" {
			owner, readErr := q.GetProjectResourceInWorkspace(ctx, db.GetProjectResourceInWorkspaceParams{ID: parseUUID(resource.ProjectResourceID), WorkspaceID: workspaceID})
			if readErr != nil {
				return readErr
			}
			if _, err := q.LockDocumentCommentProject(ctx, db.LockDocumentCommentProjectParams{ID: owner.ProjectID, WorkspaceID: workspaceID}); err != nil {
				return err
			}
			_, err = q.LockDocumentCommentProjectResource(ctx, db.LockDocumentCommentProjectResourceParams{ID: parseUUID(resource.ProjectResourceID), WorkspaceID: workspaceID})
		} else {
			switch resource.OwnerKind {
			case "issue":
				if _, err := q.LockDocumentCommentIssue(ctx, db.LockDocumentCommentIssueParams{ID: parseUUID(resource.OwnerID), WorkspaceID: workspaceID}); err != nil {
					return err
				}
			case "chat":
				if _, err := q.LockDocumentCommentChat(ctx, db.LockDocumentCommentChatParams{ID: parseUUID(resource.OwnerID), WorkspaceID: workspaceID}); err != nil {
					return err
				}
			case "task":
			default:
				return pgx.ErrNoRows
			}
			_, err = q.LockDocumentCommentTask(ctx, db.LockDocumentCommentTaskParams{ID: parseUUID(resource.TaskID), WorkspaceID: workspaceID})
		}
	default:
		return pgx.ErrNoRows
	}
	return err
}

func (h *Handler) UpdateDocumentCommentResolution(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.documentCommentRequestScope(w, r)
	if !ok {
		return
	}
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "human actor required")
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "comment id")
	if !ok {
		return
	}
	var input ResolveDocumentCommentRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&input) != nil || input.Resolved == nil || input.ExpectedRevision < 1 {
		writeError(w, http.StatusBadRequest, "resolution and expected_revision are required")
		return
	}
	resource, key, ok := h.resolveDocumentCommentResource(w, r, scope, input.Resource)
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot begin document comment transaction")
		return
	}
	defer tx.Rollback(r.Context())
	q := h.Queries.WithTx(tx)
	workspaceID := scope.workspaceID
	if err := lockDocumentCommentOwner(r.Context(), q, workspaceID, resource); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "document no longer exists")
		} else {
			writeError(w, http.StatusInternalServerError, "cannot lock document")
		}
		return
	}
	previous, err := q.LockDocumentComment(r.Context(), db.LockDocumentCommentParams{ID: id, WorkspaceID: workspaceID, ResourceKey: key})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && previous.ParentID.Valid) {
		writeError(w, http.StatusNotFound, "document discussion not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot load document discussion")
		return
	}
	if previous.Revision != input.ExpectedRevision {
		writeRevisionConflict(w, "document_comment", id, input.ExpectedRevision, previous.Revision)
		return
	}
	row, err := q.UpdateDocumentCommentResolution(r.Context(), db.UpdateDocumentCommentResolutionParams{ID: id, WorkspaceID: workspaceID, ActorID: parseUUID(scope.userID), ExpectedRevision: previous.Revision, Resolved: *input.Resolved})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot update document discussion")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "cannot commit document discussion")
		return
	}
	writeJSON(w, http.StatusOK, documentCommentResponse(row))
}
