package handler

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) FinishMeeting(w http.ResponseWriter, r *http.Request) {
	meeting, ok := h.loadMeeting(w, r)
	if !ok {
		return
	}
	if meeting.Status == "ready" {
		writeJSON(w, http.StatusOK, meetingResponse(meeting, nil, false))
		return
	}
	segments, err := h.Queries.ListMeetingSegments(r.Context(), meeting.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load meeting segments")
		return
	}
	if len(segments) == 0 {
		writeError(w, http.StatusBadRequest, "meeting has no transcript")
		return
	}
	transcript := meetingTranscript(segments)
	organization := organizeMeeting(r.Context(), h.LLM, transcript, meeting.StartedAt.Time)
	projects, err := h.Queries.ListProjects(r.Context(), db.ListProjectsParams{WorkspaceID: meeting.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to classify meeting project")
		return
	}
	project, found := chooseMeetingProject(projects, transcript+"\n"+organization.ProjectHint)
	projectID := pgtype.UUID{}
	if found {
		projectID = project.ID
	}
	if meeting.SourceChatSessionID.Valid {
		session, sessionErr := h.Queries.GetChatSessionInWorkspace(r.Context(), db.GetChatSessionInWorkspaceParams{ID: meeting.SourceChatSessionID, WorkspaceID: meeting.WorkspaceID})
		if sessionErr == nil && session.ProjectID.Valid {
			projectID = session.ProjectID
		}
	}
	decisions, err := json.Marshal(organization.Decisions)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to organize meeting decisions")
		return
	}
	actionItems, err := json.Marshal(organization.ActionItems)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to organize meeting action items")
		return
	}
	classification, err := json.Marshal(map[string]any{
		"topic":              organization.Topic,
		"project_id":         uuidToOptionalString(projectID),
		"project_confidence": map[bool]string{true: "high", false: "none"}[projectID.Valid],
		"source":             "automatic",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to classify meeting")
		return
	}
	updated, err := h.Queries.FinalizeMeetingSession(r.Context(), db.FinalizeMeetingSessionParams{
		ID: meeting.ID, WorkspaceID: meeting.WorkspaceID, Status: "ready", Title: organization.Title,
		Topic: organization.Topic, ProjectID: projectID, Transcript: transcript, Summary: organization.Summary,
		Decisions: decisions, ActionItems: actionItems, Classification: classification,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to finalize meeting")
		return
	}
	writeJSON(w, http.StatusOK, meetingResponse(updated, segments, true))
}

func (h *Handler) CancelMeeting(w http.ResponseWriter, r *http.Request) {
	meeting, ok := h.loadMeeting(w, r)
	if !ok {
		return
	}
	if meeting.Status == "ready" || meeting.Status == "cancelled" {
		writeJSON(w, http.StatusOK, meetingResponse(meeting, nil, false))
		return
	}
	updated, err := h.Queries.UpdateMeetingSessionStatus(r.Context(), db.UpdateMeetingSessionStatusParams{
		ID: meeting.ID, WorkspaceID: meeting.WorkspaceID, Status: "cancelled",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel meeting")
		return
	}
	writeJSON(w, http.StatusOK, meetingResponse(updated, nil, false))
}
