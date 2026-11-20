package handler

import (
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type CreateMeetingRequest struct {
	SourceType        string  `json:"source_type"`
	SourceChatSession *string `json:"source_chat_session_id"`
	SourceAgent       *string `json:"source_agent_id"`
	RuntimeID         *string `json:"runtime_id"`
	Language          string  `json:"language"`
	Timezone          string  `json:"timezone"`
}

type AppendMeetingSegmentRequest struct {
	Sequence     int64    `json:"sequence"`
	StartMS      int64    `json:"start_ms"`
	EndMS        int64    `json:"end_ms"`
	SpeakerLabel string   `json:"speaker_label"`
	Text         string   `json:"text"`
	IsFinal      bool     `json:"is_final"`
	Confidence   *float32 `json:"confidence"`
}

type UpdateMeetingRequest struct {
	Title       *string   `json:"title"`
	Topic       *string   `json:"topic"`
	ProjectID   *string   `json:"project_id"`
	Summary     *string   `json:"summary"`
	Transcript  *string   `json:"transcript"`
	Decisions   *[]string `json:"decisions"`
	ActionItems *[]string `json:"action_items"`
}

type ArchiveMeetingRequest struct {
	Archived bool `json:"archived"`
}

type MeetingSegmentResponse struct {
	ID           string   `json:"id"`
	Sequence     int64    `json:"sequence"`
	StartMS      int64    `json:"start_ms"`
	EndMS        int64    `json:"end_ms"`
	SpeakerLabel string   `json:"speaker_label"`
	Text         string   `json:"text"`
	IsFinal      bool     `json:"is_final"`
	Confidence   *float32 `json:"confidence,omitempty"`
}

type MeetingResponse struct {
	ID                  string                   `json:"id"`
	WorkspaceID         string                   `json:"workspace_id"`
	CreatorID           string                   `json:"creator_id"`
	SourceType          string                   `json:"source_type"`
	SourceChatSessionID string                   `json:"source_chat_session_id,omitempty"`
	SourceAgentID       string                   `json:"source_agent_id,omitempty"`
	RuntimeID           string                   `json:"runtime_id,omitempty"`
	Status              string                   `json:"status"`
	Language            string                   `json:"language"`
	Title               string                   `json:"title"`
	Timezone            string                   `json:"timezone"`
	StartedAt           string                   `json:"started_at"`
	EndedAt             string                   `json:"ended_at,omitempty"`
	AudioRetention      string                   `json:"audio_retention"`
	ProjectID           string                   `json:"project_id,omitempty"`
	Topic               string                   `json:"topic"`
	Transcript          string                   `json:"transcript,omitempty"`
	Summary             string                   `json:"summary,omitempty"`
	Decisions           json.RawMessage          `json:"decisions"`
	ActionItems         json.RawMessage          `json:"action_items"`
	Classification      json.RawMessage          `json:"classification"`
	FailureReason       string                   `json:"failure_reason,omitempty"`
	ArchivedAt          string                   `json:"archived_at,omitempty"`
	CreatedAt           string                   `json:"created_at"`
	UpdatedAt           string                   `json:"updated_at"`
	Segments            []MeetingSegmentResponse `json:"segments,omitempty"`
}

func uuidToOptionalString(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuidToString(value)
}

func optionalTimestampToString(value pgtype.Timestamptz) string {
	if !value.Valid {
		return ""
	}
	return timestampToString(value)
}

func meetingResponse(meeting db.MeetingSession, segments []db.MeetingSegment, includeSegments bool) MeetingResponse {
	response := MeetingResponse{
		ID: uuidToString(meeting.ID), WorkspaceID: uuidToString(meeting.WorkspaceID), CreatorID: uuidToString(meeting.CreatorID),
		SourceType: meeting.SourceType, SourceChatSessionID: uuidToOptionalString(meeting.SourceChatSessionID), SourceAgentID: uuidToOptionalString(meeting.SourceAgentID),
		RuntimeID: uuidToOptionalString(meeting.RuntimeID), Status: meeting.Status, Language: meeting.Language, Title: meeting.Title, Timezone: meeting.Timezone,
		StartedAt: timestampToString(meeting.StartedAt), EndedAt: optionalTimestampToString(meeting.EndedAt), AudioRetention: meeting.AudioRetention,
		ProjectID: uuidToOptionalString(meeting.ProjectID), Topic: meeting.Topic, Transcript: meeting.Transcript, Summary: meeting.Summary,
		Decisions: json.RawMessage(meeting.Decisions), ActionItems: json.RawMessage(meeting.ActionItems), Classification: json.RawMessage(meeting.Classification),
		FailureReason: meeting.FailureReason, CreatedAt: timestampToString(meeting.CreatedAt), UpdatedAt: timestampToString(meeting.UpdatedAt),
	}
	response.ArchivedAt = optionalTimestampToString(meeting.ArchivedAt)
	if includeSegments {
		response.Segments = make([]MeetingSegmentResponse, 0, len(segments))
		for _, segment := range segments {
			response.Segments = append(response.Segments, meetingSegmentResponse(segment))
		}
	}
	return response
}

func meetingSegmentResponse(segment db.MeetingSegment) MeetingSegmentResponse {
	var confidence *float32
	if segment.Confidence.Valid {
		confidence = &segment.Confidence.Float32
	}
	return MeetingSegmentResponse{ID: uuidToString(segment.ID), Sequence: segment.Sequence, StartMS: segment.StartMs, EndMS: segment.EndMs, SpeakerLabel: segment.SpeakerLabel, Text: segment.Text, IsFinal: segment.IsFinal, Confidence: confidence}
}
