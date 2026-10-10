package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func decodeExecutionRequest(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		writeError(w, 400, "invalid execution request")
		return false
	}
	return true
}

func (h *Handler) requireExecutionRuntime(w http.ResponseWriter, r *http.Request) (db.AgentRuntime, bool) {
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, chi.URLParam(r, "runtimeId"))
	if !ok {
		return runtime, false
	}
	daemonID := middleware.DaemonIDFromContext(r.Context())
	if !runtime.DaemonID.Valid || (daemonID != "" && daemonID != runtime.DaemonID.String) || (daemonID == "" && requestUserID(r) != uuidToString(runtime.OwnerID)) {
		writeError(w, 403, "runtime is not owned by this control")
		return runtime, false
	}
	return runtime, true
}

func executionIdentity(execution db.TaskExecution) protocol.ExecutionIdentity {
	return protocol.ExecutionIdentity{TaskID: uuidToString(execution.TaskID), RuntimeID: uuidToString(execution.RuntimeID), DispatchedAt: execution.DispatchedAt.Time, ExecutionID: uuidToString(execution.ExecutionID), WorkerID: uuidToString(execution.WorkerID)}
}

func checkExecutionRuntime(r *http.Request, q *db.Queries, runtime db.AgentRuntime) error {
	_, err := q.RuntimeExecutionMembership(r.Context(), db.RuntimeExecutionMembershipParams{RuntimeID: runtime.ID, WorkspaceID: runtime.WorkspaceID, DaemonID: runtime.DaemonID})
	return err
}

func lockExecutionSupervisor(r *http.Request, q *db.Queries, runtime db.AgentRuntime, epoch int64) error {
	owner, err := q.LockRuntimeSupervisor(r.Context(), runtime.ID)
	if err != nil {
		return err
	}
	if owner.Epoch != epoch || owner.WorkspaceID != runtime.WorkspaceID || owner.DaemonID != runtime.DaemonID.String {
		return pgx.ErrNoRows
	}
	return checkExecutionRuntime(r, q, runtime)
}

func writeExecutionAuthorityError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "execution authority changed")
		return
	}
	writeError(w, 500, "execution authority transaction failed")
}

// AcquireExecutionSupervisor performs a CAS transition of runtime control ownership.
func (h *Handler) AcquireExecutionSupervisor(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.requireExecutionRuntime(w, r)
	if !ok {
		return
	}
	var req protocol.SupervisorRequest
	if !decodeExecutionRequest(w, r, &req) {
		return
	}
	instance, ok := parseUUIDOrBadRequest(w, req.InstanceID, "instance_id")
	if !ok {
		return
	}
	if req.ExpectedEpoch < 0 {
		writeError(w, 400, "invalid expected epoch")
		return
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
	var owner db.RuntimeSupervisor
	if req.ExpectedEpoch == 0 {
		owner, err = q.AcquireRuntimeSupervisor(r.Context(), db.AcquireRuntimeSupervisorParams{RuntimeID: runtime.ID, WorkspaceID: runtime.WorkspaceID, DaemonID: runtime.DaemonID.String, InstanceID: instance})
	} else {
		owner, err = q.TransferRuntimeSupervisor(r.Context(), db.TransferRuntimeSupervisorParams{RuntimeID: runtime.ID, WorkspaceID: runtime.WorkspaceID, DaemonID: runtime.DaemonID.String, InstanceID: instance, ExpectedEpoch: req.ExpectedEpoch})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		owner, err = q.GetRuntimeSupervisor(r.Context(), runtime.ID)
		if err == nil && (owner.InstanceID != instance || owner.Epoch != req.ExpectedEpoch+1 || owner.DaemonID != runtime.DaemonID.String || owner.WorkspaceID != runtime.WorkspaceID) {
			err = pgx.ErrNoRows
		}
	}
	if err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	if !commitExecutionCallback(w, r, tx) {
		return
	}
	writeJSON(w, 200, protocol.SupervisorResponse{InstanceID: uuidToString(owner.InstanceID), Epoch: owner.Epoch, Capabilities: []string{protocol.ExecutionCapabilityV1}})
}

// BindTaskExecution reserves exactly one worker for a dispatched claim. A replay
// for the same worker returns the existing identity; another worker cannot win.
func (h *Handler) BindTaskExecution(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.requireExecutionRuntime(w, r)
	if !ok {
		return
	}
	taskID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "task_id")
	if !ok {
		return
	}
	var req protocol.BindExecutionRequest
	if !decodeExecutionRequest(w, r, &req) {
		return
	}
	worker, ok := parseUUIDOrBadRequest(w, req.WorkerID, "worker_id")
	if !ok {
		return
	}
	if req.DispatchedAt.IsZero() || req.DispatchedAt.Nanosecond()%1000 != 0 || req.SupervisorEpoch < 1 {
		writeError(w, 400, "invalid execution claim")
		return
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
	if task.RuntimeID != runtime.ID || !task.DispatchedAt.Valid || !task.DispatchedAt.Time.Equal(req.DispatchedAt) {
		writeExecutionAuthorityError(w, pgx.ErrNoRows)
		return
	}
	existing, lookupErr := q.GetTaskExecution(r.Context(), taskID)
	sameClaim := lookupErr == nil && existing.RuntimeID == task.RuntimeID && existing.DispatchedAt.Time.Equal(task.DispatchedAt.Time)
	if sameClaim {
		if existing.Revoked || existing.WorkerID != worker || existing.SupervisorEpoch != req.SupervisorEpoch || (task.Status != "running" && task.Status != "dispatched" && task.Status != "waiting_local_directory") {
			writeExecutionAuthorityError(w, pgx.ErrNoRows)
			return
		}
		if !commitExecutionCallback(w, r, tx) {
			return
		}
		writeJSON(w, 200, executionIdentity(existing))
		return
	}
	if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
		writeExecutionAuthorityError(w, lookupErr)
		return
	}
	if task.Status != "dispatched" && task.Status != "waiting_local_directory" {
		writeExecutionAuthorityError(w, pgx.ErrNoRows)
		return
	}
	if task.PrepareLeaseExpiresAt.Valid && !task.PrepareLeaseExpiresAt.Time.After(time.Now()) {
		writeExecutionAuthorityError(w, pgx.ErrNoRows)
		return
	}
	execution, err := q.BindTaskExecution(r.Context(), db.BindTaskExecutionParams{TaskID: task.ID, ExecutionID: parseUUID(uuid.NewString()), RuntimeID: runtime.ID, WorkspaceID: runtime.WorkspaceID, DaemonID: runtime.DaemonID.String, WorkerID: worker, DispatchedAt: task.DispatchedAt, SupervisorEpoch: req.SupervisorEpoch})
	if err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	if !commitExecutionCallback(w, r, tx) {
		return
	}
	writeJSON(w, 200, executionIdentity(execution))
}
