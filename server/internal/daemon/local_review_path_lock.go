package daemon

import (
	"context"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"path/filepath"
	"time"
)

// guardReviewPaths prevents new local-directory tasks from entering while a
// merge checks and updates a repository. Existing overlapping tasks block it.
func (l *LocalPathLocker) guardReviewPaths(paths []string) (func(), bool) {
	l.mu.Lock()
	for path := range l.shared {
		for _, candidate := range paths {
			forward, e1 := filepath.Rel(path, candidate)
			reverse, e2 := filepath.Rel(candidate, path)
			if (e1 == nil && filepath.IsLocal(forward)) || (e2 == nil && filepath.IsLocal(reverse)) {
				l.mu.Unlock()
				return nil, false
			}
		}
	}
	for path, entry := range l.locks {
		entry.mu2.Lock()
		active := entry.holderID != ""
		entry.mu2.Unlock()
		if !active {
			continue
		}
		for _, candidate := range paths {
			forward, e1 := filepath.Rel(path, candidate)
			reverse, e2 := filepath.Rel(candidate, path)
			if (e1 == nil && filepath.IsLocal(forward)) || (e2 == nil && filepath.IsLocal(reverse)) {
				l.mu.Unlock()
				return nil, false
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	release, available, err := execenv.ReserveSharedDirectoryPaths(ctx, paths)
	cancel()
	if err != nil || !available {
		l.mu.Unlock()
		return nil, false
	}
	return func() { release(); l.mu.Unlock() }, true
}
