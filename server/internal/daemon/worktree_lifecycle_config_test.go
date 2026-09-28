package daemon

import (
	"testing"
	"time"
)

func TestWorktreeStaleTTLRejectsInvalidPolicy(t *testing.T) {
	for _, input := range []string{"-1h", "0", "invalid"} {
		t.Run(input, func(t *testing.T) {
			t.Setenv("MULTICA_WORKTREE_STALE_TTL", input)
			if _, err := worktreeStaleTTLFromEnv(); err == nil {
				t.Fatal("invalid reminder policy accepted")
			}
		})
	}
	t.Setenv("MULTICA_WORKTREE_STALE_TTL", "14d")
	if ttl, err := worktreeStaleTTLFromEnv(); err != nil || ttl != 14*24*time.Hour {
		t.Fatalf("ttl=%s, error=%v", ttl, err)
	}
}
