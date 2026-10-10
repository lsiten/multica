package handler

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ListRuntimeExecutions provides a keyset inventory to compare with local workers.
// Enumeration alone never authorizes recovery or provider launch.
func (h *Handler) ListRuntimeExecutions(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.requireExecutionRuntime(w, r)
	if !ok {
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, 400, "invalid inventory page limit")
			return
		}
		limit = parsed
	}
	var after pgtype.UUID
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, ok = parseUUIDOrBadRequest(w, raw, "after")
		if !ok {
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
	if err = checkExecutionRuntime(r, q, runtime); err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	rows, err := q.ListRuntimeExecutions(r.Context(), db.ListRuntimeExecutionsParams{RuntimeID: runtime.ID, WorkspaceID: runtime.WorkspaceID, DaemonID: runtime.DaemonID.String, AfterTaskID: after, PageLimit: int32(limit + 1)})
	if err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	response := protocol.ExecutionInventoryResponse{Entries: []protocol.ExecutionInventoryEntry{}}
	if len(rows) > limit {
		rows = rows[:limit]
		response.NextCursor = uuidToString(rows[len(rows)-1].TaskID)
	}
	for _, row := range rows {
		response.Entries = append(response.Entries, protocol.ExecutionInventoryEntry{ExecutionIdentity: protocol.ExecutionIdentity{TaskID: uuidToString(row.TaskID), RuntimeID: uuidToString(row.RuntimeID), DispatchedAt: row.DispatchedAt.Time, ExecutionID: uuidToString(row.ExecutionID), WorkerID: uuidToString(row.WorkerID)}, Status: row.Status, Revoked: row.Revoked})
	}
	if !commitExecutionCallback(w, r, tx) {
		return
	}
	writeJSON(w, 200, response)
}
