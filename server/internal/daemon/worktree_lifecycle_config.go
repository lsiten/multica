package daemon

import (
	"fmt"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func worktreeStaleTTLFromEnv() (time.Duration, error) {
	ttl, err := durationFromEnv("MULTICA_WORKTREE_STALE_TTL", protocol.DefaultWorktreeStaleTTL)
	if err != nil {
		return 0, err
	}
	if ttl <= 0 {
		return 0, fmt.Errorf("MULTICA_WORKTREE_STALE_TTL must be positive")
	}
	return ttl, nil
}
