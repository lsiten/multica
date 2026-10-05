package service

import "time"

// ProgressScope identifies the existing entity whose work is being inspected.
type ProgressScope struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// ProgressIssueRef contains only workspace-readable issue context.
type ProgressIssueRef struct {
	ID             string  `json:"id"`
	Identifier     string  `json:"identifier"`
	Title          string  `json:"title"`
	Status         string  `json:"status"`
	StatusCategory string  `json:"status_category"`
	StatusName     string  `json:"status_name"`
	ProjectID      *string `json:"project_id"`
	ProjectTitle   string  `json:"project_title"`
}

// ProgressRun links to a run without disclosing private execution output.
type ProgressRun struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Since   string `json:"since"`
	Summary string `json:"summary"`
}

// ProgressAction is a server-checked member operation attached to its observed evidence.
type ProgressAction struct {
	Kind           string  `json:"kind"`
	ActorType      string  `json:"actor_type"`
	ActorID        *string `json:"actor_id"`
	NeedsMe        bool    `json:"needs_me"`
	Enabled        bool    `json:"enabled"`
	DisabledReason string  `json:"disabled_reason"`
	TargetIssueID  string  `json:"target_issue_id,omitempty"`
	RequestID      string  `json:"request_id,omitempty"`
}

// ProgressRequest references an existing permission-checked human request.
type ProgressRequest struct {
	ID          string `json:"id"`
	RecipientID string `json:"recipient_id"`
	NeedsMe     bool   `json:"needs_me"`
	ExpiresAt   string `json:"expires_at"`
}

// ProgressEntry describes one issue and independently recorded reasons to follow it up.
type ProgressEntry struct {
	Issue             ProgressIssueRef   `json:"issue"`
	Priority          string             `json:"priority"`
	AssigneeType      string             `json:"assignee_type"`
	AssigneeID        *string            `json:"assignee_id"`
	Path              []ProgressIssueRef `json:"path"`
	Group             *ProgressIssueRef  `json:"group"`
	Reasons           []string           `json:"reasons"`
	NeedsMe           bool               `json:"needs_me"`
	Attention         bool               `json:"attention"`
	WaitingChildren   int                `json:"waiting_children"`
	DirectBlockers    []ProgressIssueRef `json:"direct_blockers"`
	RootBlockers      []ProgressIssueRef `json:"root_blockers"`
	BlockedIssueCount int                `json:"blocked_issue_count"`
	AffectedGoalCount int                `json:"affected_goal_count"`
	Run               *ProgressRun       `json:"run"`
	Requests          []ProgressRequest  `json:"requests"`
	Stage             *int32             `json:"stage"`
	DueDate           *string            `json:"due_date"`
	WaitSince         string             `json:"wait_since"`
	Rank              int                `json:"rank"`
	NextStep          *IssueNextStep     `json:"next_step"`
	Actions           []ProgressAction   `json:"actions"`
	Revision          int64              `json:"revision"`
	sortPriority      int
	sortOverdue       bool
	sortWait          time.Time
}

// ProgressSummary counts issue records, not effort or inferred deliverables.
type ProgressSummary struct {
	Total            int `json:"total"`
	Done             int `json:"done"`
	Closed           int `json:"closed"`
	Open             int `json:"open"`
	OpenLeaf         int `json:"open_leaf"`
	OpenParent       int `json:"open_parent"`
	Attention        int `json:"attention"`
	PendingDecisions int `json:"pending_decisions"`
}

// IssueProgressView is a paginated, read-only projection of recorded work.
type IssueProgressView struct {
	WorkspaceID     string            `json:"workspace_id"`
	Scope           ProgressScope     `json:"scope"`
	AsOf            string            `json:"as_of"`
	Version         string            `json:"version"`
	Summary         ProgressSummary   `json:"summary"`
	Root            *ProgressEntry    `json:"root"`
	Items           []ProgressEntry   `json:"items"`
	ProjectRequests []ProgressRequest `json:"project_requests"`
	Complete        bool              `json:"complete"`
	CoverageReasons []string          `json:"coverage_reasons"`
	NextCursor      *string           `json:"next_cursor"`
	HasMore         bool              `json:"has_more"`
	FilteredTotal   int               `json:"filtered_total"`
}

// ProgressOptions controls only the new inspection surfaces.
type ProgressOptions struct {
	Scope        ProgressScope
	Filter       string
	AssigneeType string
	AssigneeID   string
	OnlyMine     bool
	Limit        int
	Cursor       string
	MemberID     string
	Today        string
}
