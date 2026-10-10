package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/service"
)

func (h *Handler) supervisionError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "project_supervision_invalid"
	switch err.Error() {
	case "agent is not available for this project":
		status = http.StatusConflict
		code = "agent_project_scope_conflict"
	case "squad is not available for this project":
		status = http.StatusConflict
		code = "squad_project_scope_conflict"
	case "agent is not a member of this squad":
		status = http.StatusConflict
		code = "agent_squad_scope_conflict"
	}
	if errors.Is(err, service.ErrProjectSupervisionForbidden) {
		status = http.StatusForbidden
		code = "project_supervision_forbidden"
	}
	if errors.Is(err, service.ErrProjectSupervisionConflict) || errors.Is(err, service.ErrTaskActorClaim) {
		status = http.StatusConflict
		code = "project_supervision_conflict"
	}
	if errors.Is(err, service.ErrProjectExecutionCapacity) {
		status = http.StatusConflict
		code = "project_execution_capacity"
	}
	writeJSON(w, status, map[string]string{"error": err.Error(), "code": code})
}
func (h *Handler) GetProjectSupervision(w http.ResponseWriter, r *http.Request) {
	project, _, ok := h.loadProjectScopeBindingTarget(w, r)
	if !ok {
		return
	}
	view, err := h.ProjectSupervisionService.View(r.Context(), project)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project supervision")
		return
	}
	writeJSON(w, http.StatusOK, view)
}
func (h *Handler) SaveProjectSupervision(w http.ResponseWriter, r *http.Request) {
	project, member, ok := h.loadProjectScopeBindingTarget(w, r)
	if !ok {
		return
	}
	if !isWorkspaceAdmin(member.Role) || r.Header.Get("X-Actor-Source") == "task_token" {
		writeError(w, http.StatusForbidden, "only workspace owners and admins may configure supervision")
		return
	}
	var input struct {
		Enabled  bool                             `json:"enabled"`
		Revision int64                            `json:"revision"`
		Config   service.ProjectSupervisionConfig `json:"config"`
	}
	input.Config = service.DefaultProjectSupervisionConfig()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid supervision policy")
		return
	}
	if err := h.ProjectSupervisionService.Save(r.Context(), project, member.UserID, input.Enabled, input.Config, input.Revision); err != nil {
		h.supervisionError(w, err)
		return
	}
	h.GetProjectSupervision(w, r)
}
func (h *Handler) CheckProjectSupervision(w http.ResponseWriter, r *http.Request) {
	project, member, ok := h.loadProjectScopeBindingTarget(w, r)
	if !ok {
		return
	}
	if !isWorkspaceAdmin(member.Role) || r.Header.Get("X-Actor-Source") == "task_token" {
		writeError(w, http.StatusForbidden, "only workspace owners and admins may check supervision")
		return
	}
	if err := h.ProjectSupervisionService.CheckNow(r.Context(), project); err != nil {
		h.supervisionError(w, err)
		return
	}
	h.GetProjectSupervision(w, r)
}
func (h *Handler) ApplyProjectSupervision(w http.ResponseWriter, r *http.Request) {
	if _, authenticated := auth.TaskActorFromContext(r.Context()); !authenticated {
		writeError(w, 403, "an authenticated task actor is required")
		return
	}
	project, _, ok := h.loadProjectScopeBindingTarget(w, r)
	if !ok {
		return
	}
	if r.Header.Get("X-Actor-Source") != "task_token" {
		writeError(w, http.StatusForbidden, "a project coordination task token is required")
		return
	}
	taskID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Task-ID"), "task id")
	if !ok {
		return
	}
	var input struct {
		CheckedVersion int64                              `json:"checked_version"`
		Actions        []service.ProjectSupervisionAction `json:"actions"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid coordination actions")
		return
	}
	count, err := h.ProjectSupervisionService.Apply(r.Context(), project, taskID, input.CheckedVersion, input.Actions)
	if err != nil {
		h.supervisionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"verified_actions": count})
}
func (h *Handler) ReportProjectSupervision(w http.ResponseWriter, r *http.Request) {
	if _, authenticated := auth.TaskActorFromContext(r.Context()); !authenticated {
		writeError(w, 403, "an authenticated task actor is required")
		return
	}
	project, _, ok := h.loadProjectScopeBindingTarget(w, r)
	if !ok {
		return
	}
	if r.Header.Get("X-Actor-Source") != "task_token" {
		writeError(w, http.StatusForbidden, "a project coordination task token is required")
		return
	}
	var input service.ProjectSupervisionReport
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid coordination report")
		return
	}
	if input.TaskID != r.Header.Get("X-Task-ID") {
		writeError(w, http.StatusForbidden, "coordination report task mismatch")
		return
	}
	if err := h.ProjectSupervisionService.Report(r.Context(), project, input); err != nil {
		h.supervisionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"accepted": true})
}
