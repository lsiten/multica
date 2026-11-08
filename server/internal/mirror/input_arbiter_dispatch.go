package mirror

import (
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// dispatch serializes native posting and release with ownership changes. Release
// callbacks must not reenter the arbiter or acquire peer locks.
func (a *Arbiter) dispatch(resource protocol.ResourceKey, principal Principal, gestureID string, apply func() string, release func()) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked(a.now())
	key := resourceKey(resource)
	lock := a.locks[key]
	if lock == nil || lock.principal != principal || lock.gestureID != gestureID {
		return protocol.MirrorInputStale
	}
	reason := apply()
	if reason != "" {
		return reason
	}
	lock.release = release
	if lock.timer == nil {
		lock.timer = time.AfterFunc(lock.expiresAt.Sub(a.now()), func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.locks[key] == lock && !a.now().Before(lock.expiresAt) {
				a.releaseLocked(key)
			}
		})
	}
	return ""
}

func (a *Arbiter) releaseLocked(key string) {
	lock := a.locks[key]
	if lock.timer != nil {
		lock.timer.Stop()
	}
	if lock.release != nil {
		lock.release()
	}
	delete(a.locks, key)
}
