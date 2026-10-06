package protocol

import "time"

// TaskGCStatus keeps the legacy GC fields while exposing authoritative lifecycle
// metadata. Older servers omit LifecycleSupported; clients must then retain work.
type TaskGCStatus struct {
	Missing               bool       `json:"missing,omitempty"`
	TaskID                string     `json:"task_id,omitempty"`
	WorkspaceID           string     `json:"workspace_id,omitempty"`
	RuntimeID             string     `json:"runtime_id,omitempty"`
	AgentID               string     `json:"agent_id,omitempty"`
	WorkDir               string     `json:"work_dir,omitempty"`
	Status                string     `json:"status"`
	CompletedAt           time.Time  `json:"completed_at"`
	LifecycleSupported    bool       `json:"lifecycle_supported"`
	IssueID               string     `json:"issue_id,omitempty"`
	ChatSessionID         string     `json:"chat_session_id,omitempty"`
	ChatStatus            string     `json:"chat_status,omitempty"`
	AutopilotRunID        string     `json:"autopilot_run_id,omitempty"`
	IssueStatus           string     `json:"issue_status,omitempty"`
	IssueStatusCategory   string     `json:"issue_status_category,omitempty"`
	LastActivityAt        *time.Time `json:"last_activity_at,omitempty"`
	RetentionSupported    bool       `json:"retention_supported,omitempty"`
	CurrentWorkDir        string     `json:"current_work_dir,omitempty"`
	CurrentTaskID         string     `json:"current_task_id,omitempty"`
	WaitingHuman          bool       `json:"waiting_human,omitempty"`
	CurrentAgent          bool       `json:"current_agent,omitempty"`
	IssueRevision         int64      `json:"issue_revision,omitempty"`
	PrepareLeaseExpiresAt *time.Time `json:"prepare_lease_expires_at,omitempty"`
	AutopilotID           string     `json:"autopilot_id,omitempty"`
}

// TaskGCBatch returns only facts scoped to an authorized workspace and runtime.
// Missing entries are unknown, never permission to delete a local directory.
type TaskGCBatch struct {
	Tasks []TaskGCStatus `json:"tasks"`
}
