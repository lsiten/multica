package mirror

import (
	"sort"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ControllerState is metadata about one currently bound human input capability.
// It deliberately contains no gesture, text, pointer, or coordinate data.
type ControllerState struct {
	ViewerID string
	UserID   string
	Source   protocol.MirrorSource
}

// ControllerStates is the stable-order active controller snapshot for one runtime.
type ControllerStates []ControllerState

// ControlStateTracker tracks active controllers per runtime. Multiple viewers
// may be active at once when they control distinct resources.
type ControlStateTracker struct {
	mu     sync.Mutex
	active map[string]map[string]ControllerState
}

func NewControlStateTracker() *ControlStateTracker {
	return &ControlStateTracker{active: make(map[string]map[string]ControllerState)}
}

// SetState applies one controller transition. changed reports a controller or
// source change; presenceChanged reports a zero-to-one or one-to-zero transition.
func (t *ControlStateTracker) SetState(runtimeID string, state ControllerState, active bool) (states ControllerStates, changed bool, presenceChanged bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	controllers := t.active[runtimeID]
	if controllers == nil {
		controllers = make(map[string]ControllerState)
		t.active[runtimeID] = controllers
	}
	wasEmpty := len(controllers) == 0
	if active {
		if current, ok := controllers[state.ViewerID]; ok && current == state {
			return nil, false, false
		}
		controllers[state.ViewerID] = state
		return t.snapshotLocked(runtimeID), true, wasEmpty
	}
	if _, ok := controllers[state.ViewerID]; !ok {
		return nil, false, false
	}
	delete(controllers, state.ViewerID)
	if len(controllers) == 0 {
		delete(t.active, runtimeID)
	}
	return t.snapshotLocked(runtimeID), true, len(controllers) == 0
}

func (t *ControlStateTracker) Snapshot(runtimeID string) ControllerStates {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotLocked(runtimeID)
}

func (t *ControlStateTracker) snapshotLocked(runtimeID string) ControllerStates {
	controllers := t.active[runtimeID]
	states := make(ControllerStates, 0, len(controllers))
	for _, state := range controllers {
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool {
		if states[i].UserID != states[j].UserID {
			return states[i].UserID < states[j].UserID
		}
		if states[i].Source.Kind != states[j].Source.Kind {
			return states[i].Source.Kind < states[j].Source.Kind
		}
		return states[i].Source.SourceID < states[j].Source.SourceID
	})
	return states
}

// ResetIf clears and returns the prior controller sets for runtimes accepted by
// shouldReset.
func (t *ControlStateTracker) ResetIf(runtimeIDs []string, shouldReset func(string) bool) map[string]ControllerStates {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]ControllerStates)
	for _, runtimeID := range runtimeIDs {
		controllers, ok := t.active[runtimeID]
		if !ok || len(controllers) == 0 {
			continue
		}
		if shouldReset != nil && !shouldReset(runtimeID) {
			continue
		}
		states := t.snapshotLocked(runtimeID)
		delete(t.active, runtimeID)
		out[runtimeID] = states
	}
	return out
}
