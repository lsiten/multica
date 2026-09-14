package vscreen

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type actionEntry struct {
	digest [32]byte
	result ActionResult
	err    error
}

// Observation is registered only from authenticated native readback, not task input.
type Observation struct {
	Lease        Lease
	Epoch        Epoch
	WindowHandle string
	Revision     uint64
}

func (a *Actor) RegisterObservation(o Observation) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.ownsLocked(o.Lease) || a.lease.Cancelled || !a.lease.ExpiresAt.After(a.clock.Now()) || !a.ready || a.frozen || o.Epoch != a.display.Epoch || o.WindowHandle == "" || o.Revision == 0 || o.Revision <= a.observations[o.WindowHandle] {
		return &Error{Reason: protocol.VscreenStaleSnapshot}
	}
	a.observations[o.WindowHandle] = o.Revision
	return nil
}

// Execute is the only action dispatch path. Cached identical retries never reach
// native input again; ambiguous results revoke authority before releasing its lock.
func (a *Actor) Execute(ctx context.Context, request Action) (ActionResult, error) {
	if err := request.Validate(); err != nil {
		return ActionResult{}, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return ActionResult{}, fmt.Errorf("encode action: %w", err)
	}
	// Clone pointer payloads so caller mutations cannot alter the dispatched command.
	var action Action
	if err = json.Unmarshal(encoded, &action); err != nil {
		return ActionResult{}, fmt.Errorf("copy action: %w", err)
	}
	digest := sha256.Sum256(encoded)
	a.nativeMu.Lock()
	defer a.nativeMu.Unlock()
	if err = ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	a.mu.Lock()
	if err = a.authorizeLocked(action); err != nil {
		a.mu.Unlock()
		return ActionResult{}, err
	}
	if cached, ok := a.actions[action.ActionID]; ok {
		a.mu.Unlock()
		if cached.digest != digest {
			return ActionResult{}, fmt.Errorf("%w: conflicting action identity", protocol.ErrInvalidVscreenContract)
		}
		return cached.result, cached.err
	}
	if a.frozen || a.stopping {
		a.mu.Unlock()
		return ActionResult{}, &Error{Reason: protocol.VscreenActionUncertainReason}
	}
	if action.Sequence != a.sequence+1 {
		a.mu.Unlock()
		return ActionResult{}, fmt.Errorf("%w: action sequence", protocol.ErrInvalidVscreenContract)
	}
	if a.observations[action.Target.WindowHandle] != action.Target.SnapshotRevision {
		a.mu.Unlock()
		return ActionResult{}, &Error{Reason: protocol.VscreenStaleSnapshot}
	}
	a.sequence = action.Sequence
	generation := a.generation
	callCtx, cancel := context.WithTimeout(ctx, ActionDeadline)
	a.cancelAction = cancel
	a.mu.Unlock()
	result, callErr := a.driver.Act(callCtx, action)
	callErr = errors.Join(callErr, callCtx.Err())
	cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancelAction = nil
	if generation != a.generation {
		return ActionResult{Epoch: action.Target.Epoch, Outcome: protocol.VscreenActionUncertain}, &Error{Reason: protocol.VscreenActionUncertainReason}
	}
	if callErr != nil || result.Epoch != a.display.Epoch || result.Outcome != protocol.VscreenActionVerified && result.Outcome != protocol.VscreenActionDispatched || a.lease.Cancelled || !a.lease.ExpiresAt.After(a.clock.Now()) {
		result = ActionResult{Epoch: action.Target.Epoch, Outcome: protocol.VscreenActionUncertain}
		callErr = &Error{Reason: protocol.VscreenActionUncertainReason, Cause: callErr}
		a.freezeLocked()
	}
	a.actions[action.ActionID] = actionEntry{digest: digest, result: result, err: callErr}
	return result, callErr
}
func (a *Actor) authorizeLocked(r Action) error {
	t := r.Target
	l := Lease{Resource: t.Resource, TaskID: t.TaskID, TransactionID: t.TransactionID, LeaseEpoch: t.LeaseEpoch, NativeEpoch: t.Epoch.NativeEpoch}
	if !a.ownsLocked(l) {
		return &Error{Reason: protocol.VscreenLeaseExpired}
	}
	if t.Epoch != a.display.Epoch {
		return &Error{Reason: protocol.VscreenStaleSnapshot}
	}
	if _, cached := a.actions[r.ActionID]; cached {
		return nil
	}
	return a.lease.ValidateAt(a.clock.Now())
}
