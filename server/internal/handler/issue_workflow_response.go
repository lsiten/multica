package handler

import (
	"time"

	"github.com/multica-ai/multica/server/internal/issueworkflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func issueWorkflowToResponse(row db.IssueWorkflow, def issueworkflow.Definition, projectIDs []string) IssueWorkflowResponse {
	if projectIDs == nil {
		projectIDs = []string{}
	}
	steps := make([]IssueWorkflowStepPayload, len(def.Steps))
	for i, s := range def.Steps {
		steps[i] = stepToPayload(s)
	}
	updatedAt := ""
	if row.UpdatedAt.Valid {
		updatedAt = row.UpdatedAt.Time.Format(time.RFC3339Nano)
	}
	return IssueWorkflowResponse{
		ID:          uuidToString(row.ID),
		WorkspaceID: uuidToString(row.WorkspaceID),
		Name:        row.Name,
		Description: row.Description,
		IsDefault:   row.IsDefault,
		OwnerID:     uuidToPtr(row.OwnerID),
		Steps:       steps,
		ProjectIDs:  projectIDs,
		CreatedAt:   timestampToString(row.CreatedAt),
		UpdatedAt:   updatedAt,
	}
}
