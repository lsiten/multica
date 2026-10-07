package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/application"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) filterReadableApplications(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID, member db.Member, applications []application.View) ([]application.View, bool) {
	actorType, actorID := h.resolveActor(r, uuidToString(member.UserID), uuidToString(workspaceID))
	if actorType != "agent" {
		return applications, true
	}
	agentID, ok := parseUUIDOrBadRequest(w, actorID, "agent_id")
	if !ok {
		return nil, false
	}
	projects := map[string]bool{}
	visible := make([]application.View, 0, len(applications))
	for _, app := range applications {
		allowed, known := projects[app.ProjectID]
		if !known {
			projectID, ok := parseUUIDOrBadRequest(w, app.ProjectID, "project_id")
			if !ok {
				return nil, false
			}
			permission, err := h.Queries.AgentMayUseProject(r.Context(), db.AgentMayUseProjectParams{LeadID: agentID, ID: projectID})
			if err != nil {
				h.applicationError(w, err)
				return nil, false
			}
			allowed = permission.Valid && permission.Bool
			projects[app.ProjectID] = allowed
		}
		if allowed {
			visible = append(visible, app)
		}
	}
	return visible, true
}
