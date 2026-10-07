package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) applicationService() *application.Service {
	return &application.Service{Queries: h.Queries, Transactions: h.TxStarter}
}

func (h *Handler) applicationScope(w http.ResponseWriter, r *http.Request) (pgtype.UUID, db.Member, bool) {
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return pgtype.UUID{}, db.Member{}, false
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	return workspaceUUID, member, ok
}

func (h *Handler) applicationActor(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID, member db.Member, projectID string) (application.Actor, bool) {
	actorType, actorID := h.resolveActor(r, uuidToString(member.UserID), uuidToString(workspaceID))
	actorUUID, ok := parseUUIDOrBadRequest(w, actorID, "actor_id")
	if !ok {
		return application.Actor{}, false
	}
	actor := application.Actor{Type: actorType, ID: actorUUID, UserID: member.UserID}
	if actorType == "agent" {
		actor.TaskID, ok = parseUUIDOrBadRequest(w, r.Header.Get("X-Task-ID"), "task_id")
		if !ok {
			return application.Actor{}, false
		}
		projectUUID, valid := parseUUIDOrBadRequest(w, projectID, "project_id")
		if !valid {
			return application.Actor{}, false
		}
		allowed, err := h.Queries.AgentMayUseProject(r.Context(), db.AgentMayUseProjectParams{LeadID: actorUUID, ID: projectUUID})
		if err != nil {
			h.applicationError(w, err)
			return application.Actor{}, false
		}
		if !allowed.Valid || !allowed.Bool {
			h.applicationError(w, application.ErrForbidden)
			return application.Actor{}, false
		}
	}
	return actor, true
}

func (h *Handler) applicationError(w http.ResponseWriter, err error) {
	if h.applicationDeletionError(w, err) {
		return
	}
	status, code, message := http.StatusInternalServerError, "application_error", "failed to manage application"
	switch {
	case errors.Is(err, application.ErrInvalid):
		status, code, message = http.StatusBadRequest, "application_invalid", err.Error()
	case errors.Is(err, application.ErrConflict):
		status, code, message = http.StatusConflict, "application_conflict", err.Error()
	case errors.Is(err, application.ErrNotFound), errors.Is(err, pgx.ErrNoRows):
		status, code, message = http.StatusNotFound, "application_not_found", "application not found"
	case errors.Is(err, application.ErrForbidden):
		status, code, message = http.StatusForbidden, "application_forbidden", "application access forbidden"
	default:
		slog.Error("application management failed", "error", err)
	}
	writeJSON(w, status, map[string]string{"error": message, "code": code})
}

func (h *Handler) applicationDeletionError(w http.ResponseWriter, err error) bool {
	var conflict *application.DeletionConflict
	if !errors.As(err, &conflict) {
		return false
	}
	blocked := make([]map[string]string, 0, len(conflict.Blockers))
	for _, app := range conflict.Blockers {
		blocked = append(blocked, map[string]string{"id": uuidToString(app.ID), "name": app.Name})
	}
	writeJSON(w, http.StatusConflict, map[string]any{"error": conflict.Error(), "code": "applications_in_use", "applications": blocked, "total": conflict.Blockers[0].Total})
	return true
}

func decodeApplicationRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid application request")
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "application request must contain one JSON value")
		return false
	}
	return true
}

func (h *Handler) applicationChanged(r *http.Request, workspaceID pgtype.UUID, actor application.Actor, id string) {
	if h.Bus != nil {
		h.Bus.Publish(events.Event{Type: protocol.EventApplicationChanged, WorkspaceID: uuidToString(workspaceID), ActorType: actor.Type, ActorID: uuidToString(actor.ID), Payload: map[string]string{"application_id": id, "workspace_id": uuidToString(workspaceID)}})
	}
}

// ListApplications lists catalog definitions with an optional project filter.
func (h *Handler) ListApplications(w http.ResponseWriter, r *http.Request) {
	workspaceID, member, ok := h.applicationScope(w, r)
	if !ok {
		return
	}
	var projectID pgtype.UUID
	if value := r.URL.Query().Get("project_id"); value != "" {
		projectID, ok = parseUUIDOrBadRequest(w, value, "project_id")
		if !ok {
			return
		}
	}
	views, err := h.applicationService().List(r.Context(), workspaceID, projectID)
	if err != nil {
		h.applicationError(w, err)
		return
	}
	views, ok = h.filterReadableApplications(w, r, workspaceID, member, views)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": views, "total": len(views)})
}

// CreateApplication creates a revisioned definition without launching a service.
func (h *Handler) CreateApplication(w http.ResponseWriter, r *http.Request) {
	workspaceID, member, ok := h.applicationScope(w, r)
	if !ok {
		return
	}
	input := struct {
		ProjectID   string                     `json:"project_id"`
		Name        string                     `json:"name"`
		Description string                     `json:"description"`
		Kind        string                     `json:"kind"`
		Config      protocol.ApplicationConfig `json:"config"`
	}{Kind: "service", Config: protocol.DefaultApplicationConfig()}
	if !decodeApplicationRequest(w, r, &input) {
		return
	}
	projectID, ok := parseUUIDOrBadRequest(w, input.ProjectID, "project_id")
	if !ok {
		return
	}
	actor, ok := h.applicationActor(w, r, workspaceID, member, input.ProjectID)
	if !ok {
		return
	}
	view, err := h.applicationService().Create(r.Context(), workspaceID, application.CreateInput{ProjectID: projectID, Name: input.Name, Description: input.Description, Kind: input.Kind, Config: input.Config}, actor)
	if err != nil {
		h.applicationError(w, err)
		return
	}
	h.applicationChanged(r, workspaceID, actor, view.ID)
	writeJSON(w, http.StatusCreated, view)
}

func (h *Handler) loadApplication(w http.ResponseWriter, r *http.Request) (application.View, pgtype.UUID, pgtype.UUID, db.Member, bool) {
	workspaceID, member, ok := h.applicationScope(w, r)
	if !ok {
		return application.View{}, pgtype.UUID{}, pgtype.UUID{}, db.Member{}, false
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "application_id")
	if !ok {
		return application.View{}, workspaceID, pgtype.UUID{}, member, false
	}
	view, err := h.applicationService().Get(r.Context(), workspaceID, id)
	if err != nil {
		h.applicationError(w, err)
		return application.View{}, workspaceID, id, member, false
	}
	readable, ok := h.filterReadableApplications(w, r, workspaceID, member, []application.View{view})
	if !ok {
		return application.View{}, workspaceID, id, member, false
	}
	if len(readable) == 0 {
		h.applicationError(w, application.ErrForbidden)
		return application.View{}, workspaceID, id, member, false
	}
	return view, workspaceID, id, member, true
}

// GetApplication reads the current catalog revision.
func (h *Handler) GetApplication(w http.ResponseWriter, r *http.Request) {
	view, _, _, _, ok := h.loadApplication(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// UpdateApplication atomically updates configuration and outgoing relationships.
func (h *Handler) UpdateApplication(w http.ResponseWriter, r *http.Request) {
	current, workspaceID, id, member, ok := h.loadApplication(w, r)
	if !ok {
		return
	}
	actor, ok := h.applicationActor(w, r, workspaceID, member, current.ProjectID)
	if !ok {
		return
	}
	var input application.UpdateInput
	if !decodeApplicationRequest(w, r, &input) {
		return
	}
	view, err := h.applicationService().Update(r.Context(), workspaceID, id, input, actor)
	if err != nil {
		h.applicationError(w, err)
		return
	}
	h.applicationChanged(r, workspaceID, actor, view.ID)
	writeJSON(w, http.StatusOK, view)
}

// DeleteApplication requires the current revision and refuses active consumers.
func (h *Handler) DeleteApplication(w http.ResponseWriter, r *http.Request) {
	current, workspaceID, id, member, ok := h.loadApplication(w, r)
	if !ok {
		return
	}
	actor, ok := h.applicationActor(w, r, workspaceID, member, current.ProjectID)
	if !ok {
		return
	}
	revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision < 1 {
		writeError(w, http.StatusBadRequest, "revision is required")
		return
	}
	endpoints, err := h.Queries.ListApplicationEndpoints(r.Context(), db.ListApplicationEndpointsParams{WorkspaceID: workspaceID, ApplicationID: id})
	if err != nil {
		h.applicationError(w, err)
		return
	}
	if err = h.applicationService().Delete(r.Context(), workspaceID, id, revision, actor); err != nil {
		h.applicationError(w, err)
		return
	}
	for _, endpoint := range endpoints {
		h.ApplicationGateway.Revoke(uuidToString(endpoint.ID))
	}
	h.applicationChanged(r, workspaceID, actor, current.ID)
	w.WriteHeader(http.StatusNoContent)
}

// PreviewApplicationPlan shows the service nodes and readiness dependencies before execution.
func (h *Handler) PreviewApplicationPlan(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, id, _, ok := h.loadApplication(w, r)
	if !ok {
		return
	}
	plan, err := h.applicationService().Plan(r.Context(), workspaceID, id)
	if err != nil {
		h.applicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}
