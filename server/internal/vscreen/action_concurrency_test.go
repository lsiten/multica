package vscreen

import (
	"context"
	"fmt"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

type countingDriver struct {
	testDriver
	inside, peak atomic.Int32
}

func (d *countingDriver) Act(_ context.Context, a Action) (ActionResult, error) {
	n := d.inside.Add(1)
	defer d.inside.Add(-1)
	for old := d.peak.Load(); n > old && !d.peak.CompareAndSwap(old, n); old = d.peak.Load() {
	}
	runtime.Gosched()
	return ActionResult{Epoch: a.Target.Epoch, Outcome: protocol.VscreenActionVerified}, nil
}
func TestLeaseConcurrentTasksOneNativeEntry(t *testing.T) {
	d := &countingDriver{}
	m := NewManager(d, SystemClock{})
	a, err := m.For(ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "rt", UID: 501})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			id := fmt.Sprint(i)
			l, err := a.Acquire(t.Context(), Transaction{TaskID: id, ID: id})
			if err != nil {
				t.Error(err)
				return
			}
			r := actionFor(t, a, l)
			if _, err = a.Execute(t.Context(), r); err != nil {
				t.Error(err)
			}
			if err = a.Release(t.Context(), l); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if d.peak.Load() != 1 {
		t.Fatalf("native concurrency=%d", d.peak.Load())
	}
	t.Logf("tasks=20 peak_native_entries=%d", d.peak.Load())
}

func TestActionWrongEpochFreezes(t *testing.T) {
	a, d, _ := fixture(t)
	l, err := a.Acquire(t.Context(), Transaction{TaskID: "one", ID: "tx"})
	if err != nil {
		t.Fatal(err)
	}
	r := actionFor(t, a, l)
	d.outcome = ""
	if _, err = a.Execute(t.Context(), r); err == nil || !a.Status().Frozen {
		t.Fatal("unknown native result did not freeze")
	}
	if _, err = a.Execute(t.Context(), r); err == nil || d.acts != 1 {
		t.Fatal("uncertain retry replayed")
	}
}

func TestManagerShutdownRevokesLease(t *testing.T) {
	d := &testDriver{}
	m := NewManager(d, SystemClock{})
	a, err := m.For(ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "rt", UID: 501})
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
	if err = m.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Heartbeat(l); err == nil || a.Status().Ready {
		t.Fatal("shutdown retained authority")
	}
}
