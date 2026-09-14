package vscreen

import (
	"context"
	"encoding/json"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestLeaseFIFOAndCancelledWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, d, _ := fixture(t)
		first, err := a.Acquire(t.Context(), Transaction{TaskID: "one", ID: "1"})
		if err != nil {
			t.Fatal(err)
		}
		cancelCtx, cancel := context.WithCancel(t.Context())
		cancelled := make(chan error, 1)
		go func() { _, e := a.Acquire(cancelCtx, Transaction{TaskID: "cancel", ID: "2"}); cancelled <- e }()
		synctest.Wait()
		next := make(chan Lease, 1)
		go func() {
			l, e := a.Acquire(t.Context(), Transaction{TaskID: "two", ID: "3"})
			if e != nil {
				t.Error(e)
			}
			next <- l
		}()
		synctest.Wait()
		cancel()
		if <-cancelled == nil {
			t.Fatal("cancel accepted")
		}
		if a.Status().Lease.TaskID != "one" {
			t.Fatal("cancel disturbed owner")
		}
		if err = a.Release(t.Context(), first); err != nil {
			t.Fatal(err)
		}
		l := <-next
		if l.TaskID != "two" || d.quiet != 1 {
			t.Fatal("wrong FIFO owner")
		}
		if err = a.Release(t.Context(), l); err != nil {
			t.Fatal(err)
		}
	})
}

type blockingDriver struct {
	testDriver
	entered, finish, quietEntered, quietFinish, revoked chan struct{}
	once                                                sync.Once
}

func (d *blockingDriver) Act(ctx context.Context, a Action) (ActionResult, error) {
	d.once.Do(func() { close(d.entered) })
	<-ctx.Done()
	close(d.revoked)
	<-d.finish
	return ActionResult{Epoch: a.Target.Epoch, Outcome: protocol.VscreenActionVerified}, nil
}
func (d *blockingDriver) Quiesce(context.Context, ResourceKey) error {
	close(d.quietEntered)
	<-d.quietFinish
	return nil
}
func TestLeaseCancelWaitsNativeBarrierAndStaleReply(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &blockingDriver{entered: make(chan struct{}), finish: make(chan struct{}), quietEntered: make(chan struct{}), quietFinish: make(chan struct{}), revoked: make(chan struct{})}
		m := NewManager(d, SystemClock{})
		key := ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "rt", UID: 501}
		a, err := m.For(key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Ensure(t.Context()); err != nil {
			t.Fatal(err)
		}
		l, err := a.Acquire(t.Context(), Transaction{TaskID: "one", ID: "tx"})
		if err != nil {
			t.Fatal(err)
		}
		request := actionFor(t, a, l)
		done := make(chan error, 1)
		go func() { _, e := a.Execute(t.Context(), request); done <- e }()
		<-d.entered
		released := make(chan error, 1)
		go func() { released <- a.Release(t.Context(), l) }()
		<-d.revoked
		if !a.Status().Stopping || !a.Status().Lease.Cancelled {
			t.Fatal("authority not revoked")
		}
		newDisplay := a.Status().Display
		newDisplay.Epoch.NativeEpoch = "new"
		newDisplay.Epoch.DisplayGeneration = "new-display"
		if err = a.Reconcile(newDisplay); err != nil {
			t.Fatal(err)
		}
		close(d.finish)
		if <-done == nil {
			t.Fatal("stale native reply accepted")
		}
		<-d.quietEntered
		if !a.Status().Frozen {
			t.Fatal("new epoch gate opened")
		}
		close(d.quietFinish)
		if err = <-released; err != nil {
			t.Fatal(err)
		}
		if a.Status().Display.Epoch != newDisplay.Epoch || !a.Status().Frozen {
			t.Fatal("stale completion mutated new epoch")
		}
	})
}

func TestLeaseTransactionCap(t *testing.T) {
	a, _, clock := fixture(t)
	l, err := a.Acquire(t.Context(), Transaction{TaskID: "one", ID: "tx"})
	if err != nil {
		t.Fatal(err)
	}
	for range 23 {
		clock.advance(5 * time.Second)
		l, err = a.Heartbeat(l)
		if err != nil {
			t.Fatal(err)
		}
	}
	clock.advance(5 * time.Second)
	if _, err = a.Heartbeat(l); err == nil {
		t.Fatal("transaction exceeded cap")
	}
}

func TestRuntimeIsolationTrace(t *testing.T) {
	d := &testDriver{outcome: protocol.VscreenActionVerified}
	m := NewManager(d, SystemClock{})
	trace := []map[string]any{}
	for _, runtime := range []string{"r1", "r2"} {
		a, err := m.For(ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: runtime, UID: 501})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Ensure(t.Context()); err != nil {
			t.Fatal(err)
		}
		for _, task := range []string{"task1", "task2"} {
			l, err := a.Acquire(t.Context(), Transaction{TaskID: task, ID: task})
			if err != nil {
				t.Fatal(err)
			}
			r := actionFor(t, a, l)
			result, err := a.Execute(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			trace = append(trace, map[string]any{"runtime": runtime, "task": task, "lease_epoch": l.LeaseEpoch, "outcome": result.Outcome})
			if err = a.Release(t.Context(), l); err != nil {
				t.Fatal(err)
			}
		}
	}
	if d.ensures != 2 || d.acts != 4 {
		t.Fatal("resource isolation failed")
	}
	if dir := os.Getenv("VSCREEN_EVIDENCE_DIR"); dir != "" {
		raw, err := json.MarshalIndent(trace, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "task-3-trace.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
