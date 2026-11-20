package protocol

// AgentCollaborationOperation is the durable command receipt. The referenced
// Run and comment remain the source of truth for execution and transcript data.
type AgentCollaborationOperation struct {
	ID            string         `json:"operation_id"`
	Operation     string         `json:"operation"`
	Status        string         `json:"status"`
	SourceTaskID  string         `json:"source_task_id,omitempty"`
	TargetAgentID string         `json:"target_agent_id,omitempty"`
	TargetTaskID  string         `json:"target_task_id,omitempty"`
	CommentID     string         `json:"comment_id,omitempty"`
	Result        map[string]any `json:"result,omitempty"`
	Error         string         `json:"error,omitempty"`
}
