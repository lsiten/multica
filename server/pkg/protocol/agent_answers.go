package protocol

type AgentAnswerChunk struct {
	ID             string `json:"id"`
	Seq            int32  `json:"seq"`
	Type           string `json:"type"`
	Content        string `json:"content"`
	Offset         int32  `json:"offset"`
	HasMoreContent bool   `json:"has_more_content"`
	CreatedAt      string `json:"created_at"`
}

type AgentAnswerPage struct {
	TaskID     string             `json:"task_id"`
	RunStatus  string             `json:"run_status"`
	Messages   []AgentAnswerChunk `json:"messages"`
	NextCursor string             `json:"next_cursor"`
	HasMore    bool               `json:"has_more"`
}
