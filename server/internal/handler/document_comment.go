package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type DocumentCommentAnchor struct {
	Quote    string `json:"quote"`
	Start    int64  `json:"start"`
	Prefix   string `json:"prefix"`
	Suffix   string `json:"suffix"`
	Revision *int64 `json:"revision,omitempty"`
}

type DocumentCommentResponse struct {
	ID         string          `json:"id"`
	Resource   json.RawMessage `json:"resource"`
	ParentID   *string         `json:"parent_id"`
	Anchor     json.RawMessage `json:"anchor"`
	Content    string          `json:"content"`
	AuthorID   string          `json:"author_id"`
	ResolvedAt *string         `json:"resolved_at"`
	ResolvedBy *string         `json:"resolved_by"`
	Revision   int64           `json:"revision"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
}

func documentCommentResponse(row db.DocumentComment) DocumentCommentResponse {
	var resolvedAt *string
	if row.ResolvedAt.Valid {
		value := row.ResolvedAt.Time.Format(time.RFC3339Nano)
		resolvedAt = &value
	}
	return DocumentCommentResponse{
		ID: uuidToString(row.ID), Resource: json.RawMessage(row.ResourceRef), ParentID: uuidToPtr(row.ParentID),
		Anchor: json.RawMessage(row.Anchor), Content: row.Content, AuthorID: uuidToString(row.AuthorID),
		ResolvedAt: resolvedAt, ResolvedBy: uuidToPtr(row.ResolvedBy), Revision: row.Revision,
		CreatedAt: row.CreatedAt.Time.Format(time.RFC3339Nano), UpdatedAt: row.UpdatedAt.Time.Format(time.RFC3339Nano),
	}
}

// QueryDocumentComments reads a bounded page, including resolved discussions.
func (h *Handler) QueryDocumentComments(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.documentCommentRequestScope(w, r)
	if !ok {
		return
	}
	var input QueryDocumentCommentsRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&input) != nil || input.Offset < 0 {
		writeError(w, http.StatusBadRequest, "invalid document comment query")
		return
	}
	_, key, ok := h.resolveDocumentCommentResource(w, r, scope, input.Resource)
	if !ok {
		return
	}
	const pageSize = 100
	rows, err := h.Queries.ListDocumentComments(r.Context(), db.ListDocumentCommentsParams{WorkspaceID: scope.workspaceID, ResourceKey: key, Limit: pageSize + 1, Offset: input.Offset})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot load document comments")
		return
	}
	more := len(rows) > pageSize
	if more {
		rows = rows[:pageSize]
	}
	result := make([]DocumentCommentResponse, 0, len(rows))
	for _, row := range rows {
		result = append(result, documentCommentResponse(row))
	}
	writeJSON(w, http.StatusOK, struct {
		Comments   []DocumentCommentResponse `json:"comments"`
		HasMore    bool                      `json:"has_more"`
		NextOffset int32                     `json:"next_offset"`
	}{Comments: result, HasMore: more, NextOffset: input.Offset + int32(len(result))})
}

func (h *Handler) CreateDocumentComment(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.documentCommentRequestScope(w, r)
	if !ok {
		return
	}
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "human actor required")
		return
	}
	var input CreateDocumentCommentRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&input) != nil || strings.TrimSpace(input.Content) == "" || utf8.RuneCountInString(input.Content) > 16000 {
		writeError(w, http.StatusBadRequest, "a document comment of at most 16000 characters is required")
		return
	}
	if input.Anchor != nil && (input.ParentID != nil || strings.TrimSpace(input.Anchor.Quote) == "" || utf8.RuneCountInString(input.Anchor.Quote) > 4000 || input.Anchor.Start < 0 || utf8.RuneCountInString(input.Anchor.Prefix) > 200 || utf8.RuneCountInString(input.Anchor.Suffix) > 200 || (input.Anchor.Revision != nil && *input.Anchor.Revision <= 0)) {
		writeError(w, http.StatusBadRequest, "invalid document selection anchor")
		return
	}
	resource, key, ok := h.resolveDocumentCommentResource(w, r, scope, input.Resource)
	if !ok {
		return
	}
	var parentID pgtype.UUID
	if input.ParentID != nil {
		parentID, ok = parseUUIDOrBadRequest(w, *input.ParentID, "parent_id")
		if !ok {
			return
		}
	}
	var submissionID pgtype.UUID
	if input.SubmissionID != nil {
		submissionID, ok = parseUUIDOrBadRequest(w, *input.SubmissionID, "submission_id")
		if !ok {
			return
		}
	}
	encodedResource, err := json.Marshal(resource)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot encode document resource")
		return
	}
	var anchor []byte
	if input.Anchor != nil {
		anchor, err = json.Marshal(input.Anchor)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cannot encode document selection")
			return
		}
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
	var parent db.DocumentComment
	if parentID.Valid {
		parent, err = q.LockDocumentComment(r.Context(), db.LockDocumentCommentParams{ID: parentID, WorkspaceID: workspaceID, ResourceKey: key})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && parent.ParentID.Valid) {
			writeError(w, http.StatusNotFound, "document discussion not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cannot load document discussion")
			return
		}
	}
	content := strings.TrimSpace(input.Content)
	row, err := q.CreateDocumentComment(r.Context(), db.CreateDocumentCommentParams{ID: submissionID, WorkspaceID: workspaceID, ResourceKey: key, ResourceRef: encodedResource, ParentID: parentID, Anchor: anchor, Content: content, AuthorID: parseUUID(scope.userID)})
	created := true
	if errors.Is(err, pgx.ErrNoRows) {
		created = false
		row, err = q.GetMatchingDocumentCommentReceipt(r.Context(), db.GetMatchingDocumentCommentReceiptParams{ID: submissionID, WorkspaceID: workspaceID, ResourceKey: key, ParentID: parentID, Anchor: anchor, Content: content, AuthorID: parseUUID(scope.userID)})
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "submission_id belongs to a different document comment")
			return
		}
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot create document comment")
		return
	}
	if created && parent.ResolvedAt.Valid {
		if _, err := q.UpdateDocumentCommentResolution(r.Context(), db.UpdateDocumentCommentResolutionParams{ID: parent.ID, WorkspaceID: workspaceID, ActorID: parseUUID(scope.userID), ExpectedRevision: parent.Revision, Resolved: false}); err != nil {
			writeError(w, http.StatusInternalServerError, "cannot reopen document discussion")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "cannot commit document comment")
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, documentCommentResponse(row))
}
