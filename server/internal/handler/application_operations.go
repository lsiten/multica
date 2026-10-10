package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) applicationInstanceChanged(workspaceID pgtype.UUID, instanceID, operationID string) {
	if h.Bus != nil {
		h.Bus.Publish(events.Event{Type: protocol.EventApplicationChanged, WorkspaceID: uuidToString(workspaceID), ActorType: "system", Payload: map[string]string{"workspace_id": uuidToString(workspaceID), "instance_id": instanceID, "operation_id": operationID}})
	}
}

// EnqueueApplicationOperation accepts a durable request after checking every affected runtime.
func (h *Handler) EnqueueApplicationOperation(w http.ResponseWriter, r *http.Request) {
	view, workspaceID, id, member, ok := h.loadApplication(w, r)
	if !ok {
		return
	}
	actor, ok := h.applicationActor(w, r, workspaceID, member, view.ProjectID)
	if !ok {
		return
	}
	var input application.OperationInput
	if !decodeApplicationRequest(w, r, &input) {
		return
	}
	operation, err := h.applicationService().Enqueue(r.Context(), workspaceID, id, input, actor)
	if err != nil {
		h.applicationError(w, err)
		return
	}
	if input.Action == "unpublish" {
		endpoints, err := h.Queries.ListApplicationEndpoints(r.Context(), db.ListApplicationEndpointsParams{WorkspaceID: workspaceID})
		if err == nil {
			for _, endpoint := range endpoints {
				for _, step := range operation.Steps {
					if endpoint.InstanceID == parseUUID(step.InstanceID) {
						h.ApplicationGateway.Revoke(uuidToString(endpoint.ID))
					}
				}
			}
		}
	}
	h.applicationChanged(r, workspaceID, actor, view.ID)
	if h.DaemonHub != nil {
		for _, step := range operation.Steps {
			h.DaemonHub.NotifyPendingWork(step.RuntimeID, protocol.PendingWorkKindApplication)
		}
	}
	writeJSON(w, http.StatusAccepted, operation)
}

// CancelApplicationOperation revokes unfinished claims and reconciles affected processes.
func (h *Handler) CancelApplicationOperation(w http.ResponseWriter, r *http.Request) {
	view, workspaceID, id, member, ok := h.loadApplication(w, r)
	if !ok {
		return
	}
	actor, ok := h.applicationActor(w, r, workspaceID, member, view.ProjectID)
	if !ok {
		return
	}
	operationID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "operationId"), "operation_id")
	if !ok {
		return
	}
	operation, err := h.applicationService().Cancel(r.Context(), workspaceID, id, operationID, actor)
	if err != nil {
		h.applicationError(w, err)
		return
	}
	h.applicationChanged(r, workspaceID, actor, view.ID)
	if h.DaemonHub != nil {
		for _, step := range operation.Steps {
			h.DaemonHub.NotifyPendingWork(step.RuntimeID, protocol.PendingWorkKindApplication)
		}
	}
	writeJSON(w, http.StatusAccepted, operation)
}

// ListApplicationOperations returns recent application activity without executable commands.
func (h *Handler) ListApplicationOperations(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, id, _, ok := h.loadApplication(w, r)
	if !ok {
		return
	}
	operations, err := h.applicationService().ListOperations(r.Context(), workspaceID, id)
	if err != nil {
		h.applicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, operations)
}

// GetApplicationOperation reads the operation only through its owning application.
func (h *Handler) GetApplicationOperation(w http.ResponseWriter, r *http.Request) {
	view, workspaceID, _, _, ok := h.loadApplication(w, r)
	if !ok {
		return
	}
	operationID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "operationId"), "operation_id")
	if !ok {
		return
	}
	operation, err := h.applicationService().GetOperation(r.Context(), workspaceID, operationID)
	if err != nil {
		h.applicationError(w, err)
		return
	}
	if operation.ApplicationID != view.ID {
		h.applicationError(w, application.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

func (h *Handler) applicationDaemonRuntime(w http.ResponseWriter, r *http.Request, daemonID string) (db.AgentRuntime, bool) {
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, chi.URLParam(r, "runtimeId"))
	if !ok {
		return db.AgentRuntime{}, false
	}
	if !protocol.ValidApplicationDaemonID(daemonID) {
		writeError(w, http.StatusBadRequest, "invalid daemon_id")
		return db.AgentRuntime{}, false
	}
	if !runtime.DaemonID.Valid || runtime.DaemonID.String != daemonID {
		h.applicationError(w, application.ErrForbidden)
		return db.AgentRuntime{}, false
	}
	if authenticatedDaemon := middleware.DaemonIDFromContext(r.Context()); authenticatedDaemon != "" {
		if authenticatedDaemon != daemonID {
			h.applicationError(w, application.ErrForbidden)
			return db.AgentRuntime{}, false
		}
	} else if !runtime.OwnerID.Valid || requestUserID(r) != uuidToString(runtime.OwnerID) {
		h.applicationError(w, application.ErrForbidden)
		return db.AgentRuntime{}, false
	}
	return runtime, true
}

// ClaimRuntimeApplications leases commands only to the daemon hosting the target runtime.
func (h *Handler) ClaimRuntimeApplications(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DaemonID string `json:"daemon_id"`
	}
	if !decodeApplicationRequest(w, r, &input) {
		return
	}
	runtime, ok := h.applicationDaemonRuntime(w, r, input.DaemonID)
	if !ok {
		return
	}
	session, ok := h.beginApplicationServiceTransaction(w, r, runtime)
	if !ok {
		return
	}
	defer session.rollback(r)
	claims, err := session.service.Claim(r.Context(), runtime.WorkspaceID, runtime.ID)
	if err != nil {
		h.applicationError(w, err)
		return
	}
	for index := range claims {
		commands := []protocol.ApplicationControlCommand{claims[index].Command}
		if err := h.applicationConnectionCommands(commands); err != nil {
			h.applicationError(w, err)
			return
		}
		claims[index].Command = commands[0]
	}
	if !session.commit(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, claims)
}

// SyncRuntimeApplications supplies the desired registry used to recover local services.
func (h *Handler) SyncRuntimeApplications(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DaemonID string `json:"daemon_id"`
	}
	if !decodeApplicationRequest(w, r, &input) {
		return
	}
	runtime, ok := h.applicationDaemonRuntime(w, r, input.DaemonID)
	if !ok {
		return
	}
	session, ok := h.beginApplicationServiceTransaction(w, r, runtime)
	if !ok {
		return
	}
	defer session.rollback(r)
	instances, err := session.service.RuntimeInstances(r.Context(), runtime.WorkspaceID, runtime.ID)
	if err != nil {
		h.applicationError(w, err)
		return
	}
	for index := range instances {
		commands := []protocol.ApplicationControlCommand{instances[index].Command}
		if err := h.applicationConnectionCommands(commands); err != nil {
			h.applicationError(w, err)
			return
		}
		instances[index].Command = commands[0]
	}
	if !session.commit(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, instances)
}

// ReportRuntimeApplication persists an observation from the instance's exact runtime.
func (h *Handler) ReportRuntimeApplication(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DaemonID    string                          `json:"daemon_id"`
		Observation protocol.ApplicationObservation `json:"observation"`
	}
	if !decodeApplicationRequest(w, r, &input) {
		return
	}
	runtime, ok := h.applicationDaemonRuntime(w, r, input.DaemonID)
	if !ok {
		return
	}
	session, ok := h.beginApplicationServiceTransaction(w, r, runtime)
	if !ok {
		return
	}
	defer session.rollback(r)
	if err := session.service.Observe(r.Context(), runtime.WorkspaceID, runtime.ID, input.Observation); err != nil {
		h.applicationError(w, err)
		return
	}
	if !session.commit(w, r) {
		return
	}
	h.applicationInstanceChanged(runtime.WorkspaceID, input.Observation.InstanceID, "")
	w.WriteHeader(http.StatusNoContent)
}

// CompleteRuntimeApplication acknowledges the exact generation and lease token that executed.
func (h *Handler) CompleteRuntimeApplication(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DaemonID string                         `json:"daemon_id"`
		Result   protocol.ApplicationStepResult `json:"result"`
	}
	if !decodeApplicationRequest(w, r, &input) {
		return
	}
	runtime, ok := h.applicationDaemonRuntime(w, r, input.DaemonID)
	if !ok {
		return
	}
	stepID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "stepId"), "step_id")
	if !ok {
		return
	}
	session, ok := h.beginApplicationServiceTransaction(w, r, runtime)
	if !ok {
		return
	}
	defer session.rollback(r)
	operationID, err := session.service.Complete(r.Context(), runtime.WorkspaceID, runtime.ID, stepID, input.Result)
	if err != nil {
		h.applicationError(w, err)
		return
	}
	if !session.commit(w, r) {
		return
	}
	h.applicationInstanceChanged(runtime.WorkspaceID, input.Result.Observation.InstanceID, uuidToString(operationID))
	writeJSON(w, http.StatusOK, map[string]string{"operation_id": uuidToString(operationID)})
}

// RenewRuntimeApplicationLease rejects stale claims before another attempt takes over.
func (h *Handler) RenewRuntimeApplicationLease(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DaemonID   string `json:"daemon_id"`
		ClaimToken string `json:"claim_token"`
	}
	if !decodeApplicationRequest(w, r, &input) {
		return
	}
	runtime, ok := h.applicationDaemonRuntime(w, r, input.DaemonID)
	if !ok {
		return
	}
	stepID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "stepId"), "step_id")
	if !ok {
		return
	}
	claimToken, ok := parseUUIDOrBadRequest(w, input.ClaimToken, "claim_token")
	if !ok {
		return
	}
	session, ok := h.beginApplicationServiceTransaction(w, r, runtime)
	if !ok {
		return
	}
	defer session.rollback(r)
	if err := session.service.RenewLease(r.Context(), runtime.WorkspaceID, runtime.ID, stepID, claimToken); err != nil {
		h.applicationError(w, err)
		return
	}
	if !session.commit(w, r) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
