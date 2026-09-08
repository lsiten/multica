package daemon

import "path/filepath"

// guardReviewPaths prevents new local-directory tasks from entering while a
// merge checks and updates a repository. Existing overlapping tasks block it.
func (l *LocalPathLocker) guardReviewPaths(paths []string) (func(), bool) {
	l.mu.Lock()
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
	return l.mu.Unlock, true
}
