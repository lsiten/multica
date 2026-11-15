package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var errRerunSourceMismatch = errors.New("rerun source mismatch")

// applyExactChatRerun never uses another chat turn's session for an explicit source.
func (h *Handler) applyExactChatRerun(ctx context.Context, task db.AgentTaskQueue, resp *AgentTaskResponse) error {
	source, err := h.Queries.GetAgentTask(ctx, task.RerunOfTaskID)
	if err != nil {
		return err
	}
	resp.PriorSessionID = ""
	resp.PriorWorkDir = ""
	if !rerunSourceMatchesTaskScope(task, source) ||
		!chatTaskProjectScopesMatch(task, source) ||
		source.ChannelContextRevision != task.ChannelContextRevision {
		return errRerunSourceMismatch
	}
	resp.PriorWorkDir = source.WorkDir.String
	if source.RuntimeID == task.RuntimeID &&
		!service.ResumeUnsafeFailure(source.FailureReason.String, source.Error.String) &&
		!source.SessionRolloutMissing {
		resp.PriorSessionID = source.SessionID.String
	}
	resp.PriorSessionResumeUnavailable = source.SessionRolloutMissing
	return nil
}

func chatTaskProjectScopesMatch(task, source db.AgentTaskQueue) bool {
	var taskContext, sourceContext struct {
		ProjectID *string `json:"project_id"`
	}
	if len(task.Context) > 0 && json.Unmarshal(task.Context, &taskContext) != nil {
		return false
	}
	if len(source.Context) > 0 && json.Unmarshal(source.Context, &sourceContext) != nil {
		return false
	}
	return strings.TrimSpace(valueOrEmpty(taskContext.ProjectID)) ==
		strings.TrimSpace(valueOrEmpty(sourceContext.ProjectID))
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
