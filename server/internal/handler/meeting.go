package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) CreateMeeting(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	creatorUUID, ok := parseUUIDOrBadRequest(w, userID, "creator id")
	if !ok {
		return
	}
	var req CreateMeetingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sourceType := strings.TrimSpace(req.SourceType)
	if sourceType == "" {
		sourceType = "workspace"
	}
	if sourceType != "workspace" && sourceType != "chat" && sourceType != "group" {
		writeError(w, http.StatusBadRequest, "invalid source_type")
		return
	}
	language := strings.TrimSpace(req.Language)
	if language == "" {
		language = "zh"
	}
	timezone := strings.TrimSpace(req.Timezone)
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	chatSessionID, ok := optionalUUID(w, req.SourceChatSession, "source_chat_session_id")
	if !ok {
		return
	}
	agentID, ok := optionalUUID(w, req.SourceAgent, "source_agent_id")
	if !ok {
		return
	}
	runtimeID, ok := optionalUUID(w, req.RuntimeID, "runtime_id")
	if !ok {
		return
	}
	if agentID.Valid {
		if _, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: workspaceUUID}); err != nil {
			writeError(w, http.StatusNotFound, "source agent not found")
			return
		}
	}
	if runtimeID.Valid {
		if _, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{ID: runtimeID, WorkspaceID: workspaceUUID}); err != nil {
			writeError(w, http.StatusNotFound, "runtime not found")
			return
		}
	}
	if chatSessionID.Valid {
		session, err := h.Queries.GetChatSessionInWorkspace(r.Context(), db.GetChatSessionInWorkspaceParams{ID: chatSessionID, WorkspaceID: workspaceUUID})
		if err != nil {
			writeError(w, http.StatusNotFound, "source chat session not found")
			return
		}
		if !agentID.Valid {
			agentID = session.AgentID
		}
		if !runtimeID.Valid {
			runtimeID = session.RuntimeID
		}
	}
	meeting, err := h.Queries.CreateMeetingSession(r.Context(), db.CreateMeetingSessionParams{
		WorkspaceID: workspaceUUID, CreatorID: creatorUUID, SourceType: sourceType,
		SourceChatSessionID: chatSessionID, SourceAgentID: agentID, RuntimeID: runtimeID,
		Language: language, Timezone: timezone,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create meeting")
		return
	}
	writeJSON(w, http.StatusCreated, meetingResponse(meeting, nil, false))
}

func (h *Handler) ListMeetings(w http.ResponseWriter, r *http.Request) {
	workspaceUUID, ok := parseUUIDOrBadRequest(w, ctxWorkspaceID(r.Context()), "workspace id")
	if !ok {
		return
	}
	limit := int32(100)
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(search)) > 200 {
		writeError(w, http.StatusBadRequest, "search query is too long")
		return
	}
	includeArchived := false
	if raw := strings.TrimSpace(r.URL.Query().Get("include_archived")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid include_archived")
			return
		}
		includeArchived = parsed
	}
	projectID, ok := meetingFilterUUID(w, r.URL.Query().Get("project_id"), "project_id")
	if !ok {
		return
	}
	agentID, ok := meetingFilterUUID(w, r.URL.Query().Get("agent_id"), "agent_id")
	if !ok {
		return
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 1 || parsed > 200 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = int32(parsed)
	}
	meetings, err := h.Queries.ListMeetingSessions(r.Context(), db.ListMeetingSessionsParams{WorkspaceID: workspaceUUID, Search: search, IncludeArchived: includeArchived, ProjectID: projectID, AgentID: agentID, Limit: limit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list meetings")
		return
	}
	hidden, ok := h.requestHiddenProjects(w, r, uuidToString(workspaceUUID))
	if !ok {
		return
	}
	responses := make([]MeetingResponse, 0, len(meetings))
	for _, meeting := range meetings {
		if !hidden.CanSeeProject(meeting.ProjectID) {
			continue
		}
		responses = append(responses, meetingResponse(meeting, nil, false))
	}
	writeJSON(w, http.StatusOK, responses)
}

func meetingFilterUUID(w http.ResponseWriter, raw string, name string) (pgtype.UUID, bool) {
	if strings.TrimSpace(raw) == "" {
		return pgtype.UUID{}, true
	}
	return parseUUIDOrBadRequest(w, strings.TrimSpace(raw), name)
}

func (h *Handler) GetMeeting(w http.ResponseWriter, r *http.Request) {
	meeting, ok := h.loadMeeting(w, r)
	if !ok {
		return
	}
	segments, err := h.Queries.ListMeetingSegments(r.Context(), meeting.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load meeting segments")
		return
	}
	writeJSON(w, http.StatusOK, meetingResponse(meeting, segments, true))
}

func (h *Handler) AppendMeetingSegment(w http.ResponseWriter, r *http.Request) {
	meeting, ok := h.loadMeeting(w, r)
	if !ok {
		return
	}
	if meeting.Status != "recording" && meeting.Status != "paused" {
		writeError(w, http.StatusConflict, "meeting is not recording")
		return
	}
	var req AppendMeetingSegmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Sequence < 0 || req.StartMS < 0 || req.EndMS < req.StartMS || strings.TrimSpace(req.Text) == "" || len([]rune(req.Text)) > 16_384 {
		writeError(w, http.StatusBadRequest, "invalid meeting segment")
		return
	}
	confidence := pgtype.Float4{}
	if req.Confidence != nil {
		confidence = pgtype.Float4{Float32: *req.Confidence, Valid: true}
	}
	segment, err := h.Queries.AppendMeetingSegment(r.Context(), db.AppendMeetingSegmentParams{
		MeetingID: meeting.ID, Sequence: req.Sequence, StartMs: req.StartMS, EndMs: req.EndMS,
		SpeakerLabel: strings.TrimSpace(req.SpeakerLabel), Text: strings.TrimSpace(req.Text), IsFinal: req.IsFinal, Confidence: confidence,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save meeting segment")
		return
	}
	writeJSON(w, http.StatusOK, meetingSegmentResponse(segment))
}

func (h *Handler) loadMeeting(w http.ResponseWriter, r *http.Request) (db.MeetingSession, bool) {
	workspaceUUID, ok := parseUUIDOrBadRequest(w, ctxWorkspaceID(r.Context()), "workspace id")
	if !ok {
		return db.MeetingSession{}, false
	}
	meetingUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "meetingId"), "meeting id")
	if !ok {
		return db.MeetingSession{}, false
	}
	meeting, err := h.Queries.GetMeetingSession(r.Context(), db.GetMeetingSessionParams{ID: meetingUUID, WorkspaceID: workspaceUUID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "meeting not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load meeting")
		}
		return db.MeetingSession{}, false
	}
	if !h.requireProjectVisible(w, r, uuidToString(workspaceUUID), meeting.ProjectID, http.StatusNotFound, "meeting not found") {
		return db.MeetingSession{}, false
	}
	return meeting, true
}

func optionalUUID(w http.ResponseWriter, raw *string, name string) (pgtype.UUID, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return pgtype.UUID{}, true
	}
	value, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(*raw), name)
	return value, ok
}

func meetingTranscript(segments []db.MeetingSegment) string {
	var builder strings.Builder
	for _, segment := range segments {
		if strings.TrimSpace(segment.Text) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString("[")
		builder.WriteString(fmt.Sprintf("%02d:%02d", segment.StartMs/60_000, (segment.StartMs/1_000)%60))
		builder.WriteString("] ")
		if strings.TrimSpace(segment.SpeakerLabel) != "" {
			builder.WriteString(strings.TrimSpace(segment.SpeakerLabel))
			builder.WriteString(": ")
		}
		builder.WriteString(strings.TrimSpace(segment.Text))
	}
	return builder.String()
}
