package handler

import (
	"encoding/base64"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"net/http"
	"time"
)

type collaborationEvent struct {
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	CreatedAt string `json:"created_at"`
}
type collaborationArtifact struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
}
type collaborationIssue struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Key           string  `json:"key"`
	Status        string  `json:"status"`
	ParentIssueID *string `json:"parent_issue_id"`
	ContextOnly   bool    `json:"context_only"`
}
type projectCollaborationEvidence struct {
	TaskID           string                  `json:"task_id"`
	SourceTaskID     *string                 `json:"source_task_id"`
	SourceIssueID    *string                 `json:"source_issue_id"`
	AgentID          string                  `json:"agent_id"`
	AgentName        string                  `json:"agent_name"`
	SourceAgentID    *string                 `json:"source_agent_id"`
	SourceAgentName  string                  `json:"source_agent_name"`
	RelationType     string                  `json:"relation_type"`
	Status           string                  `json:"status"`
	IssueID          *string                 `json:"issue_id"`
	IssueTitle       string                  `json:"issue_title"`
	IssueKey         string                  `json:"issue_key"`
	IssueStatus      string                  `json:"issue_status"`
	TaskActive       bool                    `json:"task_active"`
	SquadID          *string                 `json:"squad_id"`
	TriggerCommentID *string                 `json:"trigger_comment_id"`
	RetryOfTaskID    *string                 `json:"retry_of_task_id"`
	RerunOfTaskID    *string                 `json:"rerun_of_task_id"`
	CreatedAt        string                  `json:"created_at"`
	StartedAt        *string                 `json:"started_at"`
	CompletedAt      *string                 `json:"completed_at"`
	EventCount       int64                   `json:"event_count"`
	Artifacts        []collaborationArtifact `json:"artifacts"`
	Events           []collaborationEvent    `json:"events"`
}
type projectCollaborationEvidenceResponse struct {
	projectCollaborationDataset
	Evidence   []projectCollaborationEvidence `json:"evidence"`
	Issues     []collaborationIssue           `json:"issues"`
	Total      int                            `json:"total"`
	NextCursor *string                        `json:"next_cursor"`
}

func (h *Handler) GetProjectCollaborationEvidence(w http.ResponseWriter, r *http.Request) {
	data, ok := h.loadProjectCollaboration(w, r)
	if !ok {
		return
	}
	rows := []db.ListProjectCollaborationRunsRow{}
	edgeID := r.URL.Query().Get("edge_id")
	for _, row := range data.Runs {
		if edgeID == "" || collaborationEdgeID(row) == edgeID {
			rows = append(rows, row)
		}
	}
	total := len(rows)
	start, end := collaborationPageBounds(total, data.projectCollaborationPage)
	rows = rows[start:end]
	data.HasMore = end < total
	var nextCursor *string
	if data.HasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		encoded := base64.RawURLEncoding.EncodeToString([]byte(last.CreatedAt.Time.UTC().Format(time.RFC3339Nano) + "|" + uuidToString(last.ID)))
		nextCursor = &encoded
	}
	member, ok := h.workspaceMember(w, r, h.resolveWorkspaceID(r))
	if !ok {
		return
	}
	taskIDs := []pgtype.UUID{}
	issueIDs := []pgtype.UUID{}
	matchedIssues := map[string]bool{}
	for _, row := range rows {
		taskIDs = append(taskIDs, row.ID)
		if row.IssueID.Valid {
			issueIDs = append(issueIDs, row.IssueID)
			matchedIssues[uuidToString(row.IssueID)] = true
		}
	}
	artifacts, err := h.Queries.ListCollaborationRunArtifacts(r.Context(), db.ListCollaborationRunArtifactsParams{WorkspaceID: parseUUID(h.resolveWorkspaceID(r)), TaskIds: taskIDs})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collaboration artifacts")
		return
	}
	byRun := map[string][]collaborationArtifact{}
	for _, artifact := range artifacts {
		run := uuidToString(artifact.TaskID)
		byRun[run] = append(byRun[run], collaborationArtifact{ID: uuidToString(artifact.ID), Filename: artifact.Filename})
	}
	events, err := h.Queries.ListCollaborationRunEvents(r.Context(), db.ListCollaborationRunEventsParams{WorkspaceID: parseUUID(h.resolveWorkspaceID(r)), ProjectID: parseUUID(data.ProjectID), TaskIds: taskIDs})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collaboration events")
		return
	}
	eventsByRun := map[string][]collaborationEvent{}
	for _, event := range events {
		key := uuidToString(event.TaskID)
		eventsByRun[key] = append(eventsByRun[key], collaborationEvent{ID: uuidToString(event.ID), EventType: event.EventType, CreatedAt: timestampToString(event.CreatedAt)})
	}
	hierarchy, err := h.Queries.ListCollaborationIssueHierarchy(r.Context(), db.ListCollaborationIssueHierarchyParams{WorkspaceID: parseUUID(h.resolveWorkspaceID(r)), ProjectID: parseUUID(data.ProjectID), IsAdmin: roleAllowed(member.Role, "owner", "admin"), UserID: member.UserID, IssueIds: issueIDs})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collaboration hierarchy")
		return
	}
	issues := []collaborationIssue{}
	for _, issue := range hierarchy {
		issues = append(issues, collaborationIssue{ID: uuidToString(issue.ID), Title: issue.Title, Key: issue.IssueKey, Status: issue.Status, ParentIssueID: uuidToPtr(issue.ParentIssueID), ContextOnly: !matchedIssues[uuidToString(issue.ID)]})
	}
	evidence := []projectCollaborationEvidence{}
	for _, row := range rows {
		item := projectCollaborationEvidence{TaskID: uuidToString(row.ID), SourceTaskID: uuidToPtr(row.SourceTaskID), SourceIssueID: uuidToPtr(row.SourceIssueID), AgentID: uuidToString(row.AgentID), AgentName: row.AgentName, SourceAgentID: uuidToPtr(row.SourceAgentID), SourceAgentName: row.SourceAgentName, RelationType: row.RelationType, Status: row.Status, IssueID: uuidToPtr(row.IssueID), IssueTitle: row.IssueTitle, IssueKey: row.IssueKey, IssueStatus: row.IssueStatus, TaskActive: row.TaskActive, SquadID: uuidToPtr(row.SquadID), TriggerCommentID: uuidToPtr(row.TriggerCommentID), CreatedAt: timestampToString(row.CreatedAt), StartedAt: timestampToPtr(row.StartedAt), CompletedAt: timestampToPtr(row.CompletedAt), EventCount: row.EventCount, Artifacts: []collaborationArtifact{}}
		item.Events = []collaborationEvent{}
		if values := eventsByRun[item.TaskID]; values != nil {
			item.Events = values
		}
		if values := byRun[item.TaskID]; values != nil {
			item.Artifacts = values
		}
		if row.SourceTaskID.Valid {
			switch row.RelationType {
			case "retry":
				item.RetryOfTaskID = uuidToPtr(row.SourceTaskID)
			case "rerun":
				item.RerunOfTaskID = uuidToPtr(row.SourceTaskID)
			}
		} else if row.RelationType != "root" && !row.SourceIssueID.Valid {
			item.RelationType = "unknown"
		}
		evidence = append(evidence, item)
	}
	writeJSON(w, http.StatusOK, projectCollaborationEvidenceResponse{projectCollaborationDataset: data, Evidence: evidence, Issues: issues, Total: total, NextCursor: nextCursor})
}
