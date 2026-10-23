package handler

import (
	"encoding/json"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func chatFileTaskVisible(task db.AgentTaskQueue) bool {
	if _, coordination := service.ProjectCoordination(task); coordination {
		return false
	}
	var source struct {
		Type               string `json:"type"`
		GroupCollaboration *struct {
			ChatSessionID string `json:"chat_session_id"`
		} `json:"group_collaboration"`
	}
	if json.Unmarshal(task.Context, &source) != nil {
		return true
	}
	// Deleted sessions can leave their old context after the FK is cleared.
	if source.Type == protocol.MirrorChatTaskContextType || (source.GroupCollaboration != nil && source.GroupCollaboration.ChatSessionID != "") {
		return task.ChatSessionID.Valid
	}
	return true
}
