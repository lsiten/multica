package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ReconcileExecutions applies a complete, explicitly scoped inventory. Absence
// and unknown observations never authorize recovery. Runtime ownership is held
// through task mutations, so competing supervisors cannot both reconcile.
func (h *Handler) ReconcileExecutions(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.requireExecutionRuntime(w, r)
	if !ok {
		return
	}
	var req protocol.ReconcileExecutionsRequest
	if !decodeExecutionRequest(w, r, &req) {
		return
	}
	snapshotID, ok := parseUUIDOrBadRequest(w, req.SnapshotID, "snapshot_id")
	if !ok {
		return
	}
	if req.Page < 0 || req.Page >= 100 || len(req.Entries) > 100 || (req.Complete && req.Unknown) {
		writeError(w, 400, "invalid snapshot page")
		return
	}
	for _, entry := range req.Entries {
		if _, ok := parseUUIDOrBadRequest(w, entry.TaskID, "task_id"); !ok {
			return
		}
		if _, ok := parseUUIDOrBadRequest(w, entry.ExecutionID, "execution_id"); !ok {
			return
		}
		if _, ok := parseUUIDOrBadRequest(w, entry.WorkerID, "worker_id"); !ok {
			return
		}
		if entry.RuntimeID != uuidToString(runtime.ID) || entry.DispatchedAt.IsZero() || entry.DispatchedAt.Nanosecond()%1000 != 0 || (entry.State != "live" && entry.State != "unknown" && entry.State != "confirmed_lost") {
			writeError(w, 400, "invalid execution observation")
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
	if err = q.CreateExecutionSnapshot(r.Context(), db.CreateExecutionSnapshotParams{RuntimeID: runtime.ID, SnapshotID: snapshotID, SupervisorEpoch: req.SupervisorEpoch}); err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	snapshot, err := q.LockExecutionSnapshot(r.Context(), db.LockExecutionSnapshotParams{RuntimeID: runtime.ID, SnapshotID: snapshotID})
	if err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	if snapshot.SupervisorEpoch != req.SupervisorEpoch || time.Since(snapshot.CreatedAt.Time) > 15*time.Minute {
		writeExecutionAuthorityError(w, pgx.ErrNoRows)
		return
	}
	var pages []protocol.ReconcileExecutionsRequest
	if err = json.Unmarshal(snapshot.Pages, &pages); err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	if req.Page < snapshot.NextPage {
		if int(req.Page) >= len(pages) || !reflect.DeepEqual(pages[req.Page], req) {
			writeError(w, 409, "snapshot page payload changed")
			return
		}
	} else {
		if req.Page != snapshot.NextPage || snapshot.Complete {
			writeError(w, 409, "snapshot page out of sequence")
			return
		}
		pages = append(pages, req)
		page, err := json.Marshal([]protocol.ReconcileExecutionsRequest{req})
		if err != nil {
			writeExecutionAuthorityError(w, err)
			return
		}
		if err = q.AppendExecutionSnapshot(r.Context(), db.AppendExecutionSnapshotParams{RuntimeID: runtime.ID, SnapshotID: snapshotID, Page: page, Complete: req.Complete}); err != nil {
			writeExecutionAuthorityError(w, err)
			return
		}
	}
	response := protocol.ReconcileExecutionsResponse{SnapshotID: req.SnapshotID, Complete: pages[len(pages)-1].Complete, Results: []protocol.ExecutionReconcileResult{}}
	entries := make([]protocol.ExecutionObservation, 0)
	seen := make(map[string]bool)
	for _, page := range pages {
		if page.Unknown {
			response.Complete = false
		}
		for _, entry := range page.Entries {
			if seen[entry.TaskID] {
				writeError(w, 409, "duplicate task in snapshot")
				return
			}
			seen[entry.TaskID] = true
			entries = append(entries, entry)
		}
	}
	// Consistent lock order also covers two tasks from the same chat session.
	sort.Slice(entries, func(i, j int) bool { return entries[i].TaskID < entries[j].TaskID })
	taskIDs := make([]pgtype.UUID, 0, len(entries))
	for _, entry := range entries {
		taskIDs = append(taskIDs, parseUUID(entry.TaskID))
	}
	if _, err = q.LockExecutionChatSessions(r.Context(), taskIDs); err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	failed := make([]db.AgentTaskQueue, 0)
	for _, entry := range entries {
		result := protocol.ExecutionReconcileResult{TaskID: entry.TaskID, ExecutionID: entry.ExecutionID, Outcome: "reject"}
		taskID := parseUUID(entry.TaskID)
		task, err := q.LockTaskForExecution(r.Context(), taskID)
		if errors.Is(err, pgx.ErrNoRows) {
			response.Results = append(response.Results, result)
			continue
		}
		if err != nil {
			writeExecutionAuthorityError(w, err)
			return
		}
		execution, err := q.GetTaskExecution(r.Context(), taskID)
		if errors.Is(err, pgx.ErrNoRows) {
			response.Results = append(response.Results, result)
			continue
		}
		if err != nil {
			writeExecutionAuthorityError(w, err)
			return
		}
		same := execution.WorkspaceID == runtime.WorkspaceID && execution.DaemonID == runtime.DaemonID.String && executionIdentity(execution).ExecutionID == entry.ExecutionID && uuidToString(execution.WorkerID) == entry.WorkerID && execution.RuntimeID == runtime.ID && task.RuntimeID == runtime.ID && task.DispatchedAt.Valid && task.DispatchedAt.Time.Equal(entry.DispatchedAt) && execution.DispatchedAt.Time.Equal(entry.DispatchedAt)
		active := task.Status == "dispatched" || task.Status == "running" || task.Status == "waiting_local_directory"
		if same && !execution.Revoked && active {
			result.Outcome = "pending"
			if response.Complete && entry.State == "live" {
				if err = q.AdoptTaskExecution(r.Context(), db.AdoptTaskExecutionParams{TaskID: taskID, ExecutionID: execution.ExecutionID, SupervisorEpoch: req.SupervisorEpoch}); err != nil {
					writeExecutionAuthorityError(w, err)
					return
				}
				result.Outcome = "adopt"
			}
			if response.Complete && entry.State == "confirmed_lost" {
				terminal, err := q.FailAgentTask(r.Context(), db.FailAgentTaskParams{ID: taskID, Error: pgtype.Text{String: "worker and provider process tree confirmed lost", Valid: true}, FailureReason: pgtype.Text{String: "runtime_recovery", Valid: true}})
				if err != nil {
					writeExecutionAuthorityError(w, err)
					return
				}
				if err = service.SettleTerminalTaskState(r.Context(), q, terminal); err != nil {
					writeExecutionAuthorityError(w, err)
					return
				}
				if err = q.RevokeTaskExecution(r.Context(), db.RevokeTaskExecutionParams{TaskID: taskID, ExecutionID: execution.ExecutionID}); err != nil {
					writeExecutionAuthorityError(w, err)
					return
				}
				failed = append(failed, terminal)
				result.Outcome = "recovered"
			}
		} else if same && execution.Revoked && task.Status == "failed" && task.FailureReason.String == "runtime_recovery" && entry.State == "confirmed_lost" && response.Complete {
			result.Outcome = "recovered"
		}
		response.Results = append(response.Results, result)
	}
	if !commitExecutionCallback(w, r, tx) {
		return
	}
	h.TaskService.HandleFailedTasks(r.Context(), failed)
	writeJSON(w, 200, response)
}
