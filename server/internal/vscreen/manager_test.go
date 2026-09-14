package vscreen

import (
	"context"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"sync"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

type testDriver struct {
	mu                   sync.Mutex
	ensures, acts, quiet int
	outcome              protocol.VscreenActionOutcome
}

func (d *testDriver) Ensure(_ context.Context, k ResourceKey) (Display, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ensures++
	return Display{Resource: k, Epoch: Epoch{NativeEpoch: "n", DisplayGeneration: "d", GeometryRevision: 1}, DisplayID: 42}, nil
}
func (d *testDriver) Act(_ context.Context, a Action) (ActionResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.acts++
	return ActionResult{Epoch: a.Target.Epoch, Outcome: d.outcome}, nil
}
func (d *testDriver) Quiesce(context.Context, ResourceKey) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.quiet++
	return nil
}
func (d *testDriver) Dispose(context.Context, ResourceKey) error { return nil }
func fixture(t *testing.T) (*Actor, *testDriver, *testClock) {
	t.Helper()
	d := &testDriver{outcome: protocol.VscreenActionVerified}
	c := &testClock{now: time.Now()}
	m := NewManager(d, c)
	a, err := m.For(ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "rt", UID: 501})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	return a, d, c
}
func TestLeaseExpiryAndEnsureDedup(t *testing.T) {
	a, d, c := fixture(t)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := a.Ensure(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if d.ensures != 1 {
		t.Fatal(d.ensures)
	}
	l, err := a.Acquire(t.Context(), Transaction{TaskID: "one", ID: "tx"})
	if err != nil {
		t.Fatal(err)
	}
	c.advance(16 * time.Second)
	if _, err = a.Heartbeat(l); err == nil {
		t.Fatal("expired heartbeat accepted")
	}
	if err = a.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	next, err := a.Acquire(t.Context(), Transaction{TaskID: "two", ID: "tx2"})
	if err != nil || next.LeaseEpoch <= l.LeaseEpoch || d.quiet != 1 {
		t.Fatalf("handoff: %+v %v quiet=%d", next, err, d.quiet)
	}
}
func actionFor(t *testing.T, a *Actor, l Lease) Action {
	t.Helper()
	e := a.Status().Display.Epoch
	if err := a.RegisterObservation(Observation{Lease: l, Epoch: e, WindowHandle: "window", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	return Action{Target: protocol.VscreenActionTarget{Resource: l.Resource, TaskID: l.TaskID, TransactionID: l.TransactionID, LeaseEpoch: l.LeaseEpoch, Epoch: e, WindowHandle: "window", SnapshotRevision: 1}, ActionID: "a", Sequence: 1, Action: protocol.VscreenAction{Kind: protocol.VscreenActionKey, Key: &protocol.VscreenKeyAction{Key: "Enter"}}}
}
func TestActionRetryConflictAndUncertain(t *testing.T) {
	a, d, _ := fixture(t)
	l, err := a.Acquire(t.Context(), Transaction{TaskID: "one", ID: "tx"})
	if err != nil {
		t.Fatal(err)
	}
	r := actionFor(t, a, l)
	for range 2 {
		if _, err := a.Execute(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	if d.acts != 1 {
		t.Fatal(d.acts)
	}
	conflict := r
	conflict.Sequence = 2
	if _, err := a.Execute(t.Context(), conflict); err == nil {
		t.Fatal("conflict accepted")
	}
	d.outcome = protocol.VscreenActionUncertain
	r.ActionID = "b"
	r.Sequence = 2
	if _, err := a.Execute(t.Context(), r); err == nil {
		t.Fatal("uncertain accepted")
	}
	if !a.Status().Frozen {
		t.Fatal("not frozen")
	}
	if _, err := a.Acquire(t.Context(), Transaction{TaskID: "two", ID: "tx2"}); err == nil {
		t.Fatal("handoff while frozen")
	}
}
