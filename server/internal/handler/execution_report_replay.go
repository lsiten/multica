package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ReplayExecutionReport accepts durable worker data from the current runtime owner.
// It does not issue a grant, launch a worker or change the execution identity.
func (h *Handler) ReplayExecutionReport(w http.ResponseWriter, r *http.Request) {
	if auth.ExecutionRequestFromContext(r.Context()).GrantHash != "" {
		writeError(w, 403, "control ownership required")
		return
	}
	runtime, ok := h.requireExecutionRuntime(w, r)
	if !ok {
		return
	}
	var input struct {
		Identity  protocol.ExecutionIdentity `json:"identity"`
		Operation string                     `json:"operation"`
		Payload   json.RawMessage            `json:"payload"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || !json.Valid(input.Payload) {
		writeError(w, 400, "invalid execution report")
		return
	}
	taskID, ok := parseUUIDOrBadRequest(w, input.Identity.TaskID, "task_id")
	if !ok {
		return
	}
	executionID, ok := parseUUIDOrBadRequest(w, input.Identity.ExecutionID, "execution_id")
	if !ok {
		return
	}
	workerID, ok := parseUUIDOrBadRequest(w, input.Identity.WorkerID, "worker_id")
	if !ok {
		return
	}
	runtimeID, ok := parseUUIDOrBadRequest(w, input.Identity.RuntimeID, "runtime_id")
	if !ok {
		return
	}
	if runtimeID != runtime.ID {
		writeError(w, 403, "execution runtime mismatch")
		return
	}
	current, err := h.Queries.GetTaskExecution(r.Context(), taskID)
	if err != nil {
		writeExecutionAuthorityError(w, err)
		return
	}
	if current.RuntimeID != runtime.ID || current.ExecutionID != executionID || current.WorkerID != workerID || !current.DispatchedAt.Time.Equal(input.Identity.DispatchedAt) {
		writeError(w, 409, "execution identity changed")
		return
	}
	var callback http.HandlerFunc
	switch input.Operation {
	case "messages":
		callback = h.ReportTaskMessages
	case "usage":
		callback = h.ReportTaskUsage
	case "complete":
		callback = h.CompleteTask
	case "fail":
		callback = h.FailTask
	default:
		writeError(w, 400, "unsupported durable execution report")
		return
	}
	request := auth.ExecutionRequest{Present: true, ExecutionID: uuidToString(executionID), WorkerID: uuidToString(workerID), GrantHash: "control-replay", Operation: input.Operation, Recovered: true}
	route := chi.NewRouteContext()
	route.URLParams.Add("taskId", uuidToString(taskID))
	ctx := context.WithValue(auth.WithExecutionRequest(r.Context(), request), chi.RouteCtxKey, route)
	forward := r.Clone(ctx)
	forward.Body = io.NopCloser(bytes.NewReader(input.Payload))
	forward.ContentLength = int64(len(input.Payload))
	callback(w, forward)
}
