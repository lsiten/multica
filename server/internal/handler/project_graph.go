package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const maxProjectGraphEventData = 64 << 10

type ProjectGraphEventResponse struct {
	ID        string
	ProjectID string
	TaskID    string
	EventType string
	NodeID    string
	Data      map[string]any
	CreatedAt string
}

func projectGraphEventResponse(event db.ProjectGraphEvent) ProjectGraphEventResponse {
	var data map[string]any
	if len(event.Data) > 0 {
		_ = json.Unmarshal(event.Data, &data)
	}
	return ProjectGraphEventResponse{
		ID: event.ID.String(), ProjectID: event.ProjectID.String(),
		TaskID: event.TaskID.String(), EventType: event.EventType,
		NodeID: event.NodeID.String, Data: data,
		CreatedAt: event.CreatedAt.Time.UTC().Format(time.RFC3339Nano),
	}
}

// enrichProjectGraphEventData makes task lineage server-authoritative. The
// daemon supplies only execution facts; delegation/retry relationships come
// from the task row already protected by the task-scoped token.
func enrichProjectGraphEventData(raw json.RawMessage, task db.AgentTaskQueue) ([]byte, error) {
	data := map[string]any{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &data); err != nil || data == nil {
			return nil, fmt.Errorf("project graph event data must be a JSON object")
		}
	}
	data["agent_id"] = task.AgentID.String()
	for _, key := range []string{"delegated_from_task_id", "retry_of_task_id", "rerun_of_task_id", "squad_id"} {
		delete(data, key)
	}
	if task.DelegatedFromTaskID.Valid {
		data["delegated_from_task_id"] = task.DelegatedFromTaskID.String()
	}
	if task.RetryOfTaskID.Valid {
		data["retry_of_task_id"] = task.RetryOfTaskID.String()
	}
	if task.RerunOfTaskID.Valid {
		data["rerun_of_task_id"] = task.RerunOfTaskID.String()
	}
	if task.SquadID.Valid {
		data["squad_id"] = task.SquadID.String()
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode project graph event data: %w", err)
	}
	if len(encoded) > maxProjectGraphEventData {
		return nil, fmt.Errorf("project graph event data is too large")
	}
	return encoded, nil
}

// ListProjectGraphEvents returns the tenant-scoped event stream used by the
// Project Graph UI. Raw task output is never stored in the event payload.
func (h *Handler) ListProjectGraphEvents(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceMember(w, r, uuidToString(workspaceID), "project not found")
	if !ok {
		return
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: workspaceID}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	limit := int32(100)
	offset := int32(0)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = int32(parsed)
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 32); err == nil && parsed >= 0 {
			offset = int32(parsed)
		}
	}
	events, err := h.Queries.ListVisibleProjectGraphEvents(r.Context(), db.ListVisibleProjectGraphEventsParams{
		ProjectID: projectID, WorkspaceID: workspaceID, PageLimit: limit, PageOffset: offset,
		UserID: member.UserID, IsAdmin: roleAllowed(member.Role, "owner", "admin"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project graph")
		return
	}
	response := make([]map[string]any, 0, len(events))
	for _, event := range events {
		item := projectGraphEventResponse(db.ProjectGraphEvent{ID: event.ID, ProjectID: event.ProjectID, WorkspaceID: event.WorkspaceID, TaskID: event.TaskID, EventType: event.EventType, NodeID: event.NodeID, Data: event.Data, CreatedAt: event.CreatedAt})
		var ids []string
		var decodeErr error
		switch value := event.ArtifactAttachmentIds.(type) {
		case []byte:
			decodeErr = json.Unmarshal(value, &ids)
		case string:
			decodeErr = json.Unmarshal([]byte(value), &ids)
		default:
			raw, marshalErr := json.Marshal(value)
			decodeErr = marshalErr
			if decodeErr == nil {
				decodeErr = json.Unmarshal(raw, &ids)
			}
		}
		if decodeErr == nil && len(ids) > 0 {
			if item.Data == nil {
				item.Data = map[string]any{}
			}
			item.Data["artifact_attachment_ids"] = ids
		}
		response = append(response, map[string]any{
			"id": item.ID, "project_id": item.ProjectID, "task_id": item.TaskID,
			"event_type": item.EventType, "node_id": item.NodeID,
			"data": item.Data, "created_at": item.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": response, "limit": limit, "offset": offset})
}
