package vscreen

import (
	"context"
	"errors"
	"fmt"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Transaction binds an entire observation/action interaction to one task.
type Transaction struct{ TaskID, ID string }
type waiter struct {
	transaction Transaction
	done        chan struct{}
	lease       Lease
	err         error
}

// Acquire queues FIFO; cancellation removes only this waiter. The caller should
// bound each wait to its MCP request budget and retain its own waiting ticket.
func (a *Actor) Acquire(ctx context.Context, tx Transaction) (Lease, error) {
	if tx.TaskID == "" || tx.ID == "" {
		return Lease{}, fmt.Errorf("%w: transaction", protocol.ErrInvalidVscreenContract)
	}
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	a.mu.Lock()
	if a.closed || !a.ready || a.frozen {
		a.mu.Unlock()
		return Lease{}, &Error{Reason: protocol.VscreenActionUncertainReason}
	}
	w := &waiter{transaction: tx, done: make(chan struct{})}
	a.queue = append(a.queue, w)
	a.grantLocked()
	a.mu.Unlock()
	select {
	case <-w.done:
	case <-ctx.Done():
	}
	if err := ctx.Err(); err != nil {
		a.mu.Lock()
		for i, v := range a.queue {
			if v == w {
				a.queue = append(a.queue[:i], a.queue[i+1:]...)
				a.mu.Unlock()
				return Lease{}, ctx.Err()
			}
		}
		a.mu.Unlock()
		if w.err != nil {
			return Lease{}, w.err
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ActionDeadline)
		defer cancel()
		return Lease{}, errors.Join(err, a.Release(cleanupCtx, w.lease))
	}
	return w.lease, w.err
}
func (a *Actor) grantLocked() {
	if a.closed || a.lease != nil || a.frozen || a.stopping || len(a.queue) == 0 {
		return
	}
	w := a.queue[0]
	a.queue = a.queue[1:]
	a.generation++
	a.started = a.clock.Now()
	l := Lease{Resource: a.key, TaskID: w.transaction.TaskID, TransactionID: w.transaction.ID, LeaseEpoch: a.generation, NativeEpoch: a.display.Epoch.NativeEpoch, ExpiresAt: a.started.Add(LeaseTTL)}
	a.lease = &l
	a.sequence = 0
	a.actions = make(map[string]actionEntry)
	a.observations = make(map[string]uint64)
	w.lease = l
	close(w.done)
}
func (a *Actor) ownsLocked(l Lease) bool {
	return a.lease != nil && a.lease.Resource == l.Resource && a.lease.TaskID == l.TaskID && a.lease.TransactionID == l.TransactionID && a.lease.LeaseEpoch == l.LeaseEpoch && a.lease.NativeEpoch == l.NativeEpoch
}

// Heartbeat never revives an expired lease or extends a transaction past its cap.
func (a *Actor) Heartbeat(l Lease) (Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.ownsLocked(l) || a.frozen || a.stopping {
		return Lease{}, &Error{Reason: protocol.VscreenLeaseExpired}
	}
	now := a.clock.Now()
	if err := a.lease.ValidateAt(now); err != nil {
		return Lease{}, err
	}
	deadline := a.started.Add(TransactionLimit)
	expires := now.Add(LeaseTTL)
	if expires.After(deadline) {
		expires = deadline
	}
	if !expires.After(now) {
		return Lease{}, &Error{Reason: protocol.VscreenLeaseExpired}
	}
	a.lease.ExpiresAt = expires
	return *a.lease, nil
}

// Release revokes immediately and waits for actual native quiescence before handoff.
func (a *Actor) Release(ctx context.Context, l Lease) error {
	a.mu.Lock()
	if !a.ownsLocked(l) {
		a.mu.Unlock()
		return nil
	}
	a.lease.Cancelled = true
	a.stopping = true
	if a.cancelAction != nil {
		a.cancelAction()
	}
	a.mu.Unlock()
	a.nativeMu.Lock()
	defer a.nativeMu.Unlock()
	a.mu.Lock()
	if !a.ownsLocked(l) {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	err := errors.Join(a.driver.Quiesce(ctx, a.key), ctx.Err())
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.freezeLocked()
		return err
	}
	if a.ownsLocked(l) {
		a.lease = nil
		a.stopping = false
		a.grantLocked()
	}
	return nil
}

// Sweep expires the owner even when no subsequent input request arrives.
func (a *Actor) Sweep(ctx context.Context) error {
	a.mu.Lock()
	if a.lease == nil || a.lease.Cancelled || a.lease.ExpiresAt.After(a.clock.Now()) {
		a.mu.Unlock()
		return nil
	}
	l := *a.lease
	a.mu.Unlock()
	return a.Release(ctx, l)
}
