package mirror

import "sync"

// ViewerTracker tracks the in-memory 0-to-1 and 1-to-0 viewer lifecycle per
// runtime. It stores no screen data and resets when a daemon disconnects.
type ViewerTracker struct {
	mu     sync.Mutex
	active map[string]map[string]struct{}
}

func NewViewerTracker() *ViewerTracker {
	return &ViewerTracker{active: make(map[string]map[string]struct{})}
}

// SetViewerActiveIf reports whether the runtime transitioned between having
// zero and one-or-more viewers. An empty viewerID remains supported for
// aggregate events sent by older daemons.
func (t *ViewerTracker) SetViewerActiveIf(
	runtimeID string,
	viewerID string,
	active bool,
	isAllowed func() bool,
) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	viewers := t.active[runtimeID]
	if active {
		if viewers == nil {
			if isAllowed != nil && !isAllowed() {
				return false
			}
			viewers = make(map[string]struct{})
			t.active[runtimeID] = viewers
		}
		if _, exists := viewers[viewerID]; exists {
			return false
		}
		viewers[viewerID] = struct{}{}
		return len(viewers) == 1
	}
	if viewers == nil {
		return false
	}
	if viewerID == "" {
		hadViewers := len(viewers) > 0
		delete(t.active, runtimeID)
		return hadViewers
	}
	if _, exists := viewers[viewerID]; !exists {
		return false
	}
	delete(viewers, viewerID)
	if len(viewers) == 0 {
		delete(t.active, runtimeID)
		return true
	}
	return false
}

// Reset clears the requested runtimes and returns those which were active.
func (t *ViewerTracker) Reset(runtimeIDs []string) []string {
	return t.ResetIf(runtimeIDs, nil)
}

// ResetIf clears and returns active runtimes accepted by shouldReset. The
// predicate runs under the tracker lock to keep the connection-count check
// atomic with clearing the state.
func (t *ViewerTracker) ResetIf(runtimeIDs []string, shouldReset func(string) bool) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	active := make([]string, 0, len(runtimeIDs))
	for _, runtimeID := range runtimeIDs {
		if viewers, ok := t.active[runtimeID]; ok && len(viewers) > 0 {
			if shouldReset != nil && !shouldReset(runtimeID) {
				continue
			}
			active = append(active, runtimeID)
			delete(t.active, runtimeID)
		}
	}
	return active
}
