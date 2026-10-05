package service

import (
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// NotifyWorktreeDelivery refreshes existing task views without changing its
// execution disposition when the shared branch's delivery is settled later.
func (s *TaskService) NotifyWorktreeDelivery(task db.AgentTaskQueue, workspaceID string) {
	if s.Bus == nil {
		return
	}
	// A delivery receipt is not another execution transition: replaying
	// completed/failed events would duplicate workflow and notification effects.
	taskID := util.UUIDToString(task.ID)
	// Issue-scoped mobile subscriptions filter progress by issue_id.
	s.Bus.Publish(events.Event{
		Type: protocol.EventTaskProgress, WorkspaceID: workspaceID,
		ActorType: "system", TaskID: taskID,
		Payload: struct {
			protocol.TaskProgressPayload
			IssueID string `json:"issue_id,omitempty"`
		}{protocol.TaskProgressPayload{TaskID: taskID, Summary: "Shared branch delivery finalized"}, util.UUIDToString(task.IssueID)},
	})
}
