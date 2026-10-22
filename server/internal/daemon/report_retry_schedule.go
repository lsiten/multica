package daemon

import (
	"crypto/sha256"
	"strings"
	"sync"
	"time"
)

type reportRetryState struct {
	payload [32]byte
	next    time.Time
	delay   time.Duration
}

// Only unchanged failed payloads back off. New reports are always eligible.
type reportRetrySchedule struct {
	mu      sync.Mutex
	entries map[string]reportRetryState
	now     func() time.Time
}

func (s *reportRetrySchedule) ready(key string, payload []byte) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.entries[key]
	return !ok || state.payload != sha256.Sum256(payload) || !s.now().Before(state.next)
}

func (s *reportRetrySchedule) record(key string, payload []byte, failed bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !failed {
		delete(s.entries, key)
		return
	}
	digest := sha256.Sum256(payload)
	state := s.entries[key]
	if state.payload != digest || state.delay == 0 {
		state.delay = terminalReportReplayInitialBackoff
	} else {
		state.delay = min(state.delay*2, terminalReportReplayMaxBackoff)
	}
	state.payload = digest
	state.next = s.now().Add(state.delay)
	s.entries[key] = state
}

func (s *reportRetrySchedule) retain(prefix string, keys map[string]struct{}) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.entries {
		if _, exists := keys[key]; strings.HasPrefix(key, prefix) && !exists {
			delete(s.entries, key)
		}
	}
}
