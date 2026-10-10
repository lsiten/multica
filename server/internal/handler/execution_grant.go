package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// IssueExecutionGrant issues or refreshes a bounded transport credential after
// rechecking current control, worker, membership, claim, and report validity.
// Refresh creates a new secret and can atomically revoke the previous hash.
func (h *Handler) IssueExecutionGrant(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.requireExecutionRuntime(w, r)
	if !ok {
		return
	}
	taskID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "task_id")
	if !ok {
		return
	}
	var req protocol.ExecutionGrantRequest
	if !decodeExecutionRequest(w, r, &req) {
		return
	}
	executionID, ok := parseUUIDOrBadRequest(w, req.ExecutionID, "execution_id")
	if !ok {
		return
	}
	revokeOnly := len(req.Operations) == 0 && req.RevokeTokenHash != ""
	if len(req.Operations) == 0 && !revokeOnly {
		writeError(w, 400, "operations required")
		return
	}
	for _, op := range req.Operations {
		if !protocol.ExecutionOperationAllowed("POST", op) && !protocol.ExecutionOperationAllowed("GET", op) {
			writeError(w, 400, "unsupported execution operation")
			return
		}
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	q := h.Queries.WithTx(tx)
	if err = lockExecutionSupervisor(r, q, runtime, req.SupervisorEpoch); err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	task, err := q.LockTaskForExecution(r.Context(), taskID)
	if err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	execution, err := q.GetTaskExecution(r.Context(), taskID)
	if err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	if execution.ExecutionID != executionID || execution.RuntimeID != runtime.ID || execution.SupervisorEpoch != req.SupervisorEpoch || task.RuntimeID != runtime.ID || !task.DispatchedAt.Time.Equal(execution.DispatchedAt.Time) || execution.Revoked {
		writeExecutionAuthorityError(w, pgx.ErrNoRows)
		return
	}
	expires := time.Now().Add(15 * time.Minute)
	if task.CompletedAt.Valid {
		deadline := task.CompletedAt.Time.Add(24 * time.Hour)
		if !deadline.After(time.Now()) {
			writeExecutionAuthorityError(w, pgx.ErrNoRows)
			return
		}
		if expires.After(deadline) {
			expires = deadline
		}
	}
	if req.RevokeTokenHash != "" {
		if err = q.RevokeExecutionGrant(r.Context(), db.RevokeExecutionGrantParams{TaskID: taskID, ExecutionID: executionID, TokenHash: req.RevokeTokenHash}); err != nil {
			writeExecutionAuthorityError(w, err)
			return
		}
	}
	if revokeOnly {
		if commitExecutionCallback(w, r, tx) {
			w.WriteHeader(204)
		}
		return
	}
	token, err := auth.GenerateExecutionGrant()
	if err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	_, err = q.CreateExecutionGrant(r.Context(), db.CreateExecutionGrantParams{TokenHash: auth.HashToken(token), TaskID: taskID, ExecutionID: executionID, Operations: req.Operations, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}})
	if err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	if !commitExecutionCallback(w, r, tx) {
		return
	}
	writeJSON(w, 200, protocol.ExecutionGrantResponse{Token: token, ExpiresAt: expires, Identity: executionIdentity(execution)})
}
