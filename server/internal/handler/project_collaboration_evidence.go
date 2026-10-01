package handler

import "net/http"

type projectCollaborationEvidence struct {
	TaskID           string  `json:"task_id"`
	SourceTaskID     *string `json:"source_task_id"`
	AgentID          string  `json:"agent_id"`
	SourceAgentID    *string `json:"source_agent_id"`
	RelationType     string  `json:"relation_type"`
	Status           string  `json:"status"`
	IssueID          *string `json:"issue_id"`
	SquadID          *string `json:"squad_id"`
	TriggerCommentID *string `json:"trigger_comment_id"`
	RetryOfTaskID    *string `json:"retry_of_task_id"`
	RerunOfTaskID    *string `json:"rerun_of_task_id"`
	CreatedAt        string  `json:"created_at"`
	StartedAt        *string `json:"started_at"`
	CompletedAt      *string `json:"completed_at"`
	EventCount       int64   `json:"event_count"`
}
type projectCollaborationEvidenceResponse struct {
	projectCollaborationDataset
	Evidence []projectCollaborationEvidence `json:"evidence"`
	Total    int                            `json:"total"`
}

func (h *Handler) GetProjectCollaborationEvidence(w http.ResponseWriter, r *http.Request) {
	data, ok := h.loadProjectCollaboration(w, r)
	if !ok {
		return
	}
	edgeID := r.URL.Query().Get("edge_id")
	evidence := make([]projectCollaborationEvidence, 0, len(data.Runs))
	for _, row := range data.Runs {
		if edgeID != "" && collaborationEdgeID(row) != edgeID {
			continue
		}
		item := projectCollaborationEvidence{
			TaskID: uuidToString(row.ID), SourceTaskID: uuidToPtr(row.SourceTaskID), AgentID: uuidToString(row.AgentID), SourceAgentID: uuidToPtr(row.SourceAgentID), RelationType: row.RelationType,
			Status: row.Status, IssueID: uuidToPtr(row.IssueID), SquadID: uuidToPtr(row.SquadID), TriggerCommentID: uuidToPtr(row.TriggerCommentID),
			CreatedAt: timestampToString(row.CreatedAt), StartedAt: timestampToPtr(row.StartedAt), CompletedAt: timestampToPtr(row.CompletedAt), EventCount: row.EventCount,
		}
		// Hidden/deleted source identifiers must not leak through lineage metadata.
		if row.SourceTaskID.Valid {
			switch row.RelationType {
			case "retry":
				item.RetryOfTaskID = uuidToPtr(row.SourceTaskID)
			case "rerun":
				item.RerunOfTaskID = uuidToPtr(row.SourceTaskID)
			}
		} else if row.RelationType != "root" {
			item.RelationType = "unknown"
		}
		evidence = append(evidence, item)
	}
	total := len(evidence)
	start, end := collaborationPageBounds(total, data.projectCollaborationPage)
	data.HasMore = end < total
	writeJSON(w, http.StatusOK, projectCollaborationEvidenceResponse{projectCollaborationDataset: data, Evidence: evidence[start:end], Total: total})
}
