package protocol

import (
	"testing"
	"time"
)

func TestWorktreeStalenessUsesLatestBusinessActivity(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	old := now.Add(-10 * 24 * time.Hour)
	recent := now.Add(-time.Hour)
	for _, tc := range []struct {
		name                string
		completed, activity *time.Time
		action              WorktreeNextAction
		want                bool
	}{
		{"old completion", &old, nil, WorktreeReview, true},
		{"recent business activity", &old, &recent, WorktreeReview, false},
		{"recent run", &recent, &old, WorktreeReview, false},
		{"missing completion", nil, &old, WorktreeUnknown, false},
		{"active", &old, &old, WorktreeActive, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lifecycle := WorktreeLifecycle{CompletedAt: tc.completed, LastActivityAt: tc.activity, NextAction: tc.action}
			if got := WorktreeStale(lifecycle, now, DefaultWorktreeStaleTTL); got != tc.want {
				t.Fatalf("stale=%t, want %t", got, tc.want)
			}
		})
	}
}
