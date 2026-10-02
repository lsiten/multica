package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type executionScopeBindingsRequest struct {
	AgentIDs []string `json:"agent_ids"`
	SquadIDs []string `json:"squad_ids"`
}

type executionScopeBindingsResponse struct {
	AgentIDs []string `json:"agent_ids"`
	SquadIDs []string `json:"squad_ids"`
}

func (h *Handler) GetProjectExecutionScopeBindings(w http.ResponseWriter, r *http.Request) {
	project, member, ok := h.loadProjectScopeBindingTarget(w, r)
	if !ok || member.Role == "" {
		return
	}
	agents, err := h.Queries.ListProjectAgentBindings(r.Context(), project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list project agent bindings")
		return
	}
	squads, err := h.Queries.ListProjectSquadBindings(r.Context(), project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list project squad bindings")
		return
	}
	resp := executionScopeBindingsResponse{}
	for _, row := range agents {
		resp.AgentIDs = append(resp.AgentIDs, uuidToString(row.AgentID))
	}
	for _, row := range squads {
		resp.SquadIDs = append(resp.SquadIDs, uuidToString(row.SquadID))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) UpdateProjectExecutionScopeBindings(w http.ResponseWriter, r *http.Request) {
	project, member, ok := h.loadProjectScopeBindingTarget(w, r)
	if !ok || !isWorkspaceAdmin(member.Role) {
		if ok {
			writeError(w, http.StatusForbidden, "only workspace owners and admins may manage project bindings")
		}
		return
	}
	var req executionScopeBindingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	workspaceID := project.WorkspaceID
	agentIDs := make([]pgtype.UUID, 0, len(req.AgentIDs))
	for _, raw := range req.AgentIDs {
		id, valid := parseUUIDOrBadRequest(w, raw, "agent_id")
		if !valid {
			return
		}
		if _, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: id, WorkspaceID: workspaceID}); err != nil {
			writeError(w, http.StatusBadRequest, "agent is not in workspace")
			return
		}
		agentIDs = append(agentIDs, id)
	}
	squadIDs := make([]pgtype.UUID, 0, len(req.SquadIDs))
	for _, raw := range req.SquadIDs {
		id, valid := parseUUIDOrBadRequest(w, raw, "squad_id")
		if !valid {
			return
		}
		if _, err := h.Queries.GetSquadInWorkspace(r.Context(), db.GetSquadInWorkspaceParams{ID: id, WorkspaceID: workspaceID}); err != nil {
			writeError(w, http.StatusBadRequest, "squad is not in workspace")
			return
		}
		squadIDs = append(squadIDs, id)
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start binding transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if err := qtx.DeleteProjectAgentBindings(r.Context(), project.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to replace agent bindings")
		return
	}
	if err := qtx.DeleteProjectSquadBindings(r.Context(), project.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to replace squad bindings")
		return
	}
	creator := member.UserID
	for _, id := range agentIDs {
		if err := qtx.UpsertAgentProjectBinding(r.Context(), db.UpsertAgentProjectBindingParams{AgentID: id, ProjectID: project.ID, CreatedBy: creator}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save agent binding")
			return
		}
	}
	for _, id := range squadIDs {
		if err := qtx.UpsertSquadProjectBinding(r.Context(), db.UpsertSquadProjectBindingParams{SquadID: id, ProjectID: project.ID, CreatedBy: creator}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save squad binding")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit project bindings")
		return
	}
	writeJSON(w, http.StatusOK, executionScopeBindingsResponse{AgentIDs: req.AgentIDs, SquadIDs: req.SquadIDs})
}

func (h *Handler) loadProjectScopeBindingTarget(w http.ResponseWriter, r *http.Request) (db.Project, db.Member, bool) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.Project{}, db.Member{}, false
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return db.Project{}, db.Member{}, false
	}
	project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: wsUUID})
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "project not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load project")
		}
		return db.Project{}, db.Member{}, false
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	return project, member, ok
}

func isWorkspaceAdmin(role string) bool { return role == "owner" || role == "admin" }
