package protocol

import "time"

type WorktreeNextAction string

const (
	WorktreeActive           WorktreeNextAction = "active"
	WorktreeReview           WorktreeNextAction = "review"
	WorktreeChangesRequested WorktreeNextAction = "changes_requested"
	WorktreeMerge            WorktreeNextAction = "merge"
	WorktreeCleanup          WorktreeNextAction = "cleanup"
	WorktreeRetained         WorktreeNextAction = "retained"
	WorktreeUnknown          WorktreeNextAction = "unknown"
	DefaultWorktreeStaleTTL                     = 7 * 24 * time.Hour
)

type WorktreeRepositoryLifecycle struct {
	Path        string             `json:"path"`
	Target      string             `json:"target,omitempty"`
	ReviewState string             `json:"review_state,omitempty"`
	NextAction  WorktreeNextAction `json:"next_action"`
	Reason      string             `json:"reason"`
}

// WorktreeLifecycle is guidance, never authorization to mutate Git or delete data.
type WorktreeLifecycle struct {
	RunStatus           string                        `json:"run_status,omitempty"`
	IssueID             string                        `json:"issue_id,omitempty"`
	IssueStatus         string                        `json:"issue_status,omitempty"`
	IssueStatusCategory string                        `json:"issue_status_category,omitempty"`
	CompletedAt         *time.Time                    `json:"completed_at,omitempty"`
	LastActivityAt      *time.Time                    `json:"last_activity_at,omitempty"`
	Stale               bool                          `json:"stale"`
	NextAction          WorktreeNextAction            `json:"next_action"`
	RepositoriesDetails []WorktreeRepositoryLifecycle `json:"repositories_details"`
}

func WorktreeStale(lifecycle WorktreeLifecycle, now time.Time, ttl time.Duration) bool {
	if ttl <= 0 || lifecycle.NextAction == WorktreeActive || lifecycle.CompletedAt == nil {
		return false
	}
	at := *lifecycle.CompletedAt
	if lifecycle.LastActivityAt != nil && lifecycle.LastActivityAt.After(at) {
		at = *lifecycle.LastActivityAt
	}
	return !at.IsZero() && now.Sub(at) > ttl
}
