package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) UpdateMeeting(w http.ResponseWriter, r *http.Request) {
	meeting, ok := h.loadMeeting(w, r)
	if !ok {
		return
	}
	if !meetingEditable(meeting.Status) {
		writeError(w, http.StatusConflict, "meeting cannot be edited in its current state")
		return
	}
	var req UpdateMeetingRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid meeting update")
		return
	}

	title := meeting.Title
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
		if len([]rune(title)) > 200 {
			writeError(w, http.StatusBadRequest, "meeting title is too long")
			return
		}
	}
	topic := meeting.Topic
	if req.Topic != nil {
		topic = strings.TrimSpace(*req.Topic)
		if len([]rune(topic)) > 120 {
			writeError(w, http.StatusBadRequest, "meeting topic is too long")
			return
		}
	}
	summary := meeting.Summary
	if req.Summary != nil {
		summary = strings.TrimSpace(*req.Summary)
		if len([]rune(summary)) > 4_000 {
			writeError(w, http.StatusBadRequest, "meeting summary is too long")
			return
		}
	}
	transcript := meeting.Transcript
	if req.Transcript != nil {
		transcript = strings.TrimSpace(*req.Transcript)
		if len([]rune(transcript)) > 200_000 {
			writeError(w, http.StatusBadRequest, "meeting transcript is too long")
			return
		}
	}
	projectID := meeting.ProjectID
	if req.ProjectID != nil {
		parsed, valid := optionalUUID(w, req.ProjectID, "project_id")
		if !valid {
			return
		}
		if parsed.Valid {
			if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: parsed, WorkspaceID: meeting.WorkspaceID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					writeError(w, http.StatusNotFound, "project not found")
				} else {
					writeError(w, http.StatusInternalServerError, "failed to validate project")
				}
				return
			}
		}
		projectID = parsed
	}
	decisions, ok := meetingListJSON(w, meeting.Decisions, req.Decisions, "decisions")
	if !ok {
		return
	}
	actionItems, ok := meetingListJSON(w, meeting.ActionItems, req.ActionItems, "action_items")
	if !ok {
		return
	}
	updated, err := h.Queries.UpdateMeetingSessionMetadata(r.Context(), db.UpdateMeetingSessionMetadataParams{
		ID: meeting.ID, WorkspaceID: meeting.WorkspaceID, Title: title, Topic: topic, ProjectID: projectID,
		Summary: summary, Transcript: transcript, Decisions: decisions, ActionItems: actionItems,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update meeting")
		return
	}
	writeJSON(w, http.StatusOK, meetingResponse(updated, nil, false))
}

func (h *Handler) ArchiveMeeting(w http.ResponseWriter, r *http.Request) {
	meeting, ok := h.loadMeeting(w, r)
	if !ok {
		return
	}
	if !meetingEditable(meeting.Status) {
		writeError(w, http.StatusConflict, "meeting cannot be archived in its current state")
		return
	}
	var req ArchiveMeetingRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid archive request")
		return
	}
	archivedAt := pgtype.Timestamptz{}
	if req.Archived {
		archivedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	}
	updated, err := h.Queries.SetMeetingSessionArchived(r.Context(), db.SetMeetingSessionArchivedParams{ID: meeting.ID, WorkspaceID: meeting.WorkspaceID, ArchivedAt: archivedAt})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to archive meeting")
		return
	}
	writeJSON(w, http.StatusOK, meetingResponse(updated, nil, false))
}

func (h *Handler) DeleteMeeting(w http.ResponseWriter, r *http.Request) {
	meeting, ok := h.loadMeeting(w, r)
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete meeting")
		return
	}
	defer tx.Rollback(r.Context())
	queries := h.Queries.WithTx(tx)
	if err := queries.DeleteMeetingDistributionsByMeeting(r.Context(), db.DeleteMeetingDistributionsByMeetingParams{MeetingID: meeting.ID, WorkspaceID: meeting.WorkspaceID}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete meeting")
		return
	}
	if err := queries.DeleteMeetingSegmentsByMeeting(r.Context(), meeting.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete meeting")
		return
	}
	if err := queries.DeleteMeetingSession(r.Context(), db.DeleteMeetingSessionParams{ID: meeting.ID, WorkspaceID: meeting.WorkspaceID}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete meeting")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete meeting")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func meetingEditable(status string) bool {
	return status == "ready" || status == "failed" || status == "cancelled"
}

func meetingListJSON(w http.ResponseWriter, current []byte, requested *[]string, field string) ([]byte, bool) {
	if requested == nil {
		return current, true
	}
	if len(*requested) > 100 {
		writeError(w, http.StatusBadRequest, field+" has too many items")
		return nil, false
	}
	for _, item := range *requested {
		if len([]rune(strings.TrimSpace(item))) > 2_000 {
			writeError(w, http.StatusBadRequest, field+" item is too long")
			return nil, false
		}
	}
	encoded, err := json.Marshal(*requested)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+field)
		return nil, false
	}
	return encoded, true
}
