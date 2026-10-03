package protocol

import "time"

// TaskGCStatus keeps the legacy GC fields while exposing authoritative lifecycle
// metadata. Older servers omit LifecycleSupported; clients must then retain work.
type TaskGCStatus struct {
	WorkspaceID         string     `json:"workspace_id,omitempty"`
	RuntimeID           string     `json:"runtime_id,omitempty"`
	AgentID             string     `json:"agent_id,omitempty"`
	WorkDir             string     `json:"work_dir,omitempty"`
	Status              string     `json:"status"`
	CompletedAt         time.Time  `json:"completed_at"`
	LifecycleSupported  bool       `json:"lifecycle_supported"`
	IssueID             string     `json:"issue_id,omitempty"`
	ChatSessionID       string     `json:"chat_session_id,omitempty"`
	AutopilotRunID      string     `json:"autopilot_run_id,omitempty"`
	IssueStatus         string     `json:"issue_status,omitempty"`
	IssueStatusCategory string     `json:"issue_status_category,omitempty"`
	LastActivityAt      *time.Time `json:"last_activity_at,omitempty"`
}
