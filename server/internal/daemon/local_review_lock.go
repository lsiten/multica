package daemon

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// The gate preserves operation/maintenance exclusion while letting disconnected
// readers leave the wait queue without spawning a goroutine or polling a mutex.
type reviewOperationGate struct {
	semaphore *semaphore.Weighted
}

var localReviewOperations = reviewOperationGate{semaphore: semaphore.NewWeighted(1)}

func (gate *reviewOperationGate) Lock(ctx context.Context) error {
	return gate.semaphore.Acquire(ctx, 1)
}

func (gate *reviewOperationGate) TryLock() bool {
	return gate.semaphore.TryAcquire(1)
}

func (gate *reviewOperationGate) Unlock() {
	gate.semaphore.Release(1)
}
