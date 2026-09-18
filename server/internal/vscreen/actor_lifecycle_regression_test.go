package vscreen

import (
	"context"
	"errors"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"testing"
	"testing/synctest"
	"time"
)

type actorLateVerifiedDriver struct{ testDriver }

func (d *actorLateVerifiedDriver) Act(ctx context.Context, a Action) (ActionResult, error) {
	<-ctx.Done()
	return ActionResult{Epoch: a.Target.Epoch, Outcome: protocol.VscreenActionVerified}, nil
}
func TestActorLateVerifiedAfterDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _, _ := fixture(t)
		a.driver = &actorLateVerifiedDriver{}
		l, err := a.Acquire(t.Context(), Transaction{TaskID: "review", ID: "late"})
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		result, err := a.Execute(t.Context(), actionFor(t, a, l))
		t.Logf("elapsed=%s outcome=%s err=%v frozen=%v", time.Since(started), result.Outcome, err, a.Status().Frozen)
		if !errors.Is(err, context.DeadlineExceeded) || result.Outcome != protocol.VscreenActionUncertain || !a.Status().Frozen {
			t.Fatal("late verified result accepted after action deadline without freezing")
		}
	})
}
func TestActorHeldAfterManagerClose(t *testing.T) {
	d := &testDriver{outcome: protocol.VscreenActionVerified}
	m := NewManager(d, SystemClock{})
	a, err := m.For(ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "rt", UID: 501})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	display, ensureErr := a.Ensure(t.Context())
	recoverErr := a.Recover(t.Context(), display.Epoch)
	lease, acquireErr := a.Acquire(t.Context(), Transaction{TaskID: "after-close", ID: "tx"})
	t.Logf("ensures=%d ensureErr=%v recoverErr=%v acquireErr=%v ready=%v lease=%+v", d.ensures, ensureErr, recoverErr, acquireErr, a.Status().Ready, lease)
	if ensureErr == nil || recoverErr == nil || acquireErr == nil {
		t.Fatal("held actor recreated display and regained authority after manager shutdown")
	}
}

// Done is evaluated only after Acquire has synchronously granted its waiter.
// Cancelling here makes the handoff and cancellation channels ready together.
type actorSimultaneousContext struct {
	context.Context
	cancel context.CancelFunc
}

func (c actorSimultaneousContext) Done() <-chan struct{} { c.cancel(); return c.Context.Done() }
func TestActorCancelledAcquireSimultaneousGrant(t *testing.T) {
	accepted := 0
	for i := 0; i < 100; i++ {
		a, _, _ := fixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		lease, err := a.Acquire(actorSimultaneousContext{ctx, cancel}, Transaction{TaskID: "cancelled", ID: "tx"})
		if ctx.Err() == nil {
			t.Fatal("probe did not cancel")
		}
		if err != nil {
			if !errors.Is(err, context.Canceled) || a.Status().Lease.TaskID != "" || a.Status().Waiting != 0 {
				t.Fatalf("cancel cleanup failed: %v %+v", err, a.Status())
			}
			next, nextErr := a.Acquire(t.Context(), Transaction{TaskID: "next", ID: "tx-next"})
			if nextErr != nil {
				t.Fatal(nextErr)
			}
			if releaseErr := a.Release(t.Context(), next); releaseErr != nil {
				t.Fatal(releaseErr)
			}
		}
		if err == nil {
			accepted++
			if a.Status().Lease.TaskID != "cancelled" {
				t.Fatal("missing occupancy")
			}
			if _, heartbeatErr := a.Heartbeat(lease); heartbeatErr != nil {
				t.Fatal(heartbeatErr)
			}
			if err = a.Release(context.Background(), lease); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Logf("cancelled acquire returned usable occupying lease in %d/100 simultaneous-ready handoffs", accepted)
	if accepted > 0 {
		t.Fatal("cancelled acquire returns live authority")
	}
}

func TestActorExplicitDisposeCanReenable(t *testing.T) {
	a, d, _ := fixture(t)
	if err := a.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	display, err := a.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Recover(t.Context(), display.Epoch); err != nil {
		t.Fatal(err)
	}
	lease, err := a.Acquire(t.Context(), Transaction{TaskID: "reenabled", ID: "tx"})
	if err != nil || d.ensures != 2 || lease.TaskID != "reenabled" {
		t.Fatalf("ensure count=%d lease=%+v err=%v", d.ensures, lease, err)
	}
	t.Log("explicit dispose -> ensure -> recover -> acquire remains available")
}

type actorDeadlineQuiesceDriver struct{ testDriver }

func (d *actorDeadlineQuiesceDriver) Quiesce(ctx context.Context, _ ResourceKey) error {
	<-ctx.Done()
	return nil
}
func TestActorCancelledAcquireBoundsQuiescence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _, _ := fixture(t)
		a.driver = &actorDeadlineQuiesceDriver{}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		start := time.Now()
		_, err := a.Acquire(actorSimultaneousContext{ctx, cancel}, Transaction{TaskID: "cancelled", ID: "tx"})
		if !errors.Is(err, context.Canceled) || !errors.Is(err, context.DeadlineExceeded) || !a.Status().Frozen || !a.Status().Lease.Cancelled || time.Since(start) != ActionDeadline {
			t.Fatalf("err=%v status=%+v elapsed=%v", err, a.Status(), time.Since(start))
		}
		if _, err := a.Acquire(t.Context(), Transaction{TaskID: "next", ID: "tx"}); err == nil {
			t.Fatal("unconfirmed quiescence granted next lease")
		}
		t.Log("cancel cleanup bounded at 3s; late nil quiescence result freezes authority")
	})
}

type actorRecoveryBarrierDriver struct {
	testDriver
	entered, finish chan struct{}
}

func (d *actorRecoveryBarrierDriver) Quiesce(ctx context.Context, key ResourceKey) error {
	if err := d.testDriver.Quiesce(ctx, key); err != nil {
		return err
	}
	d.mu.Lock()
	first := d.quiet == 1
	d.mu.Unlock()
	if first {
		close(d.entered)
		<-d.finish
	}
	return nil
}
func TestActorShutdownDuringRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &actorRecoveryBarrierDriver{entered: make(chan struct{}), finish: make(chan struct{})}
		m := NewManager(d, SystemClock{})
		a, err := m.For(ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "rt", UID: 501})
		if err != nil {
			t.Fatal(err)
		}
		display, err := a.Ensure(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		recovered := make(chan error, 1)
		go func() { recovered <- a.Recover(t.Context(), display.Epoch) }()
		<-d.entered
		m.revokeAll()
		close(d.finish)
		if err := <-recovered; err == nil {
			t.Fatal("recovery thawed actor during manager shutdown")
		}
		if err := m.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := a.Reconcile(display); err == nil {
			t.Fatal("late reconcile revived closed actor")
		}
		if _, err := a.Ensure(t.Context()); err == nil || a.Status().Ready || !a.Status().Frozen {
			t.Fatal("closed actor revived")
		}
		t.Log("shutdown remains irreversible when an in-flight recovery completes late")
	})
}
