package mirror

import (
	"context"
	"time"
)

// CloseForDaemon closes active signaling sessions owned by daemonID for the
// requested runtime set. shouldClose can additionally verify that no
// overlapping authenticated daemon connection is still serving a runtime.
// Every closed session has its offer and answer removed from memory immediately.
func (s *SessionStore) CloseForDaemon(
	ctx context.Context,
	daemonID string,
	runtimeIDs []string,
	shouldClose func(runtimeID string) bool,
) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if daemonID == "" || len(runtimeIDs) == 0 {
		return 0, nil
	}
	runtimeSet := make(map[string]struct{}, len(runtimeIDs))
	for _, runtimeID := range runtimeIDs {
		if runtimeID != "" {
			runtimeSet[runtimeID] = struct{}{}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	closed := 0
	for _, stored := range s.items {
		if stored.metadata.DaemonID != daemonID {
			continue
		}
		if _, ok := runtimeSet[stored.metadata.RuntimeID]; !ok {
			continue
		}
		if shouldClose != nil && !shouldClose(stored.metadata.RuntimeID) {
			continue
		}
		switch stored.metadata.State {
		case SessionStateOffered, SessionStateAnswered:
			stored.metadata.State = SessionStateClosed
			stored.clearSignalingData()
			closed++
		case SessionStateFailed, SessionStateClosed, SessionStateExpired:
			// Terminal records retain no SDP and are removed by the TTL janitor.
		}
	}
	return closed, nil
}

// CloseViewer closes the active signaling session owned by one negotiated
// viewer after its DataChannel or PeerConnection ends. It does not affect
// concurrent viewers connected to the same runtime.
func (s *SessionStore) CloseViewer(
	ctx context.Context,
	daemonID string,
	runtimeID string,
	viewerID string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if daemonID == "" || runtimeID == "" || viewerID == "" {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, stored := range s.items {
		if stored.metadata.DaemonID != daemonID ||
			stored.metadata.RuntimeID != runtimeID ||
			stored.metadata.ViewerID != viewerID {
			continue
		}
		switch stored.metadata.State {
		case SessionStateOffered, SessionStateAnswered:
			stored.metadata.State = SessionStateClosed
			stored.clearSignalingData()
			return true, nil
		case SessionStateFailed, SessionStateClosed, SessionStateExpired:
			return false, nil
		}
	}
	return false, nil
}

// PurgeExpired removes sessions whose short signaling TTL has elapsed. The
// store contains SDP only during this bounded handshake window.
func (s *SessionStore) PurgeExpired(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.purgeExpiredLocked(s.clock.Now()), nil
}

func (s *SessionStore) purgeExpiredLocked(now time.Time) int {
	expiredIDs := make([]string, 0)
	for id, stored := range s.items {
		if !now.Before(stored.metadata.ExpiresAt) {
			expiredIDs = append(expiredIDs, id)
		}
	}
	for _, id := range expiredIDs {
		delete(s.items, id)
	}
	return len(expiredIDs)
}

func (s *SessionStore) deleteExpiredLocked(sessionID string, now time.Time) bool {
	stored, ok := s.items[sessionID]
	if !ok || now.Before(stored.metadata.ExpiresAt) {
		return false
	}
	delete(s.items, sessionID)
	return true
}

// RunPurgeLoop periodically removes expired signaling sessions until ctx is
// canceled.
func (s *SessionStore) RunPurgeLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = defaultMirrorSessionTTL
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.PurgeExpired(ctx)
		}
	}
}
