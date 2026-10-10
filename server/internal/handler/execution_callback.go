package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func markExecutionCallback(r *http.Request) {
	request := auth.ExecutionRequestFromContext(r.Context())
	if request.Present {
		return
	}
	request.Present = true
	segments := strings.Split(r.URL.Path, "/")
	request.Operation = segments[len(segments)-1]
	*r = *r.WithContext(auth.WithExecutionRequest(r.Context(), request))
}

func writeExecutionConflict(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, service.ErrExecutionStale) {
		return false
	}
	writeError(w, http.StatusConflict, service.ErrExecutionStale.Error())
	return true
}

func (h *Handler) beginExecutionCallback(w http.ResponseWriter, r *http.Request, taskID pgtype.UUID) (pgx.Tx, *db.Queries, bool) {
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "begin execution callback")
		return nil, nil, false
	}
	q := h.Queries.WithTx(tx)
	if _, err = q.LockChatSessionForTask(r.Context(), taskID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		tx.Rollback(r.Context())
		writeError(w, 500, "lock execution session")
		return nil, nil, false
	}
	if err = service.LockExecutionCallback(r.Context(), q, taskID); err != nil {
		tx.Rollback(r.Context())
		if !writeExecutionConflict(w, err) {
			writeError(w, 500, "check execution callback")
		}
		return nil, nil, false
	}
	return tx, q, true
}

func commitExecutionCallback(w http.ResponseWriter, r *http.Request, tx pgx.Tx) bool {
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "commit execution callback")
		return false
	}
	return true
}

// ExtendExecutionPrepareLease lets a worker renew only its bound task's lease.
// The older runtime-scoped endpoint remains available for legacy control clients.
func (h *Handler) ExtendExecutionPrepareLease(w http.ResponseWriter, r *http.Request) {
	task, ok := h.requireDaemonTaskAccess(w, r, chi.URLParam(r, "taskId"))
	if !ok {
		return
	}
	updated, err := h.TaskService.ExtendTaskPrepareLease(r.Context(), task.ID, task.RuntimeID)
	if err != nil {
		if !writeExecutionConflict(w, err) {
			writeExecutionAuthorityError(w, err)
		}
		return
	}
	writeJSON(w, 200, map[string]any{"status": updated.Status})
}

// decodeScopedExecutionBody keeps legacy decoding while bounding the new closed
// execution contract and refusing trailing or undeclared input fields.
func decodeScopedExecutionBody(w http.ResponseWriter, r *http.Request, value any, limit int64) bool {
	decoder := json.NewDecoder(r.Body)
	scoped := auth.ExecutionRequestFromContext(r.Context()).GrantHash != ""
	if scoped {
		decoder = json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(value); err != nil {
		writeError(w, 400, "invalid request body")
		return false
	}
	if scoped {
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			writeError(w, 400, "invalid request body")
			return false
		}
	}
	return true
}

// commitScopedExecutionWrite rechecks actual grant expiry after any business
// lock wait. The task and membership locks remain held through this commit.
func commitScopedExecutionWrite(w http.ResponseWriter, r *http.Request, tx pgx.Tx, q *db.Queries, taskID pgtype.UUID) bool {
	if err := service.LockExecutionCallback(r.Context(), q, taskID); err != nil {
		if !writeExecutionConflict(w, err) {
			writeError(w, 500, "check execution callback")
		}
		return false
	}
	return commitExecutionCallback(w, r, tx)
}
