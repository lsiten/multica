package mirror

import (
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func testResource(uid uint32) protocol.ResourceKey {
	return protocol.ResourceKey{
		BackendIdentity: "https://multica.example",
		WorkspaceID:     "ws-1",
		RuntimeID:       "runtime-1",
		UID:             uid,
	}
}

var human = Principal{Kind: PrincipalHuman, ID: "viewer-1"}
var otherHuman = Principal{Kind: PrincipalHuman, ID: "viewer-2"}
var agent = Principal{Kind: PrincipalAgent, ID: "task-1"}

func TestArbiterFCFSSameResource(t *testing.T) {
	clock := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	a := newArbiterWithClock(func() time.Time { return clock }, time.Second)
	res := testResource(1)

	if got := a.Acquire(res, human, "g1", time.Second); got != AcquireHeld {
		t.Fatalf("first acquire = %v, want held", got)
	}
	// A different contender on the same resource is busy.
	if got := a.Acquire(res, agent, "g2", time.Second); got != AcquireBusy {
		t.Fatalf("contending acquire = %v, want busy", got)
	}
	if got := a.Acquire(res, otherHuman, "g3", time.Second); got != AcquireBusy {
		t.Fatalf("second human acquire = %v, want busy", got)
	}
	// Re-entry by the same gesture renews.
	if got := a.Acquire(res, human, "g1", time.Second); got != AcquireReentry {
		t.Fatalf("reentry = %v, want reentry", got)
	}
	// Same principal but a different gesture is still contention.
	if got := a.Acquire(res, human, "gX", time.Second); got != AcquireBusy {
		t.Fatalf("same principal new gesture = %v, want busy", got)
	}
	if a.ActiveGestures() != 1 {
		t.Fatalf("active gestures = %d, want 1", a.ActiveGestures())
	}
}

func TestArbiterDifferentResourcesDoNotContend(t *testing.T) {
	a := NewArbiter()
	if got := a.Acquire(testResource(1), human, "g1", 0); got != AcquireHeld {
		t.Fatalf("resource 1 = %v", got)
	}
	if got := a.Acquire(testResource(2), agent, "g2", 0); got != AcquireHeld {
		t.Fatalf("resource 2 should be independent, got %v", got)
	}
	if a.ActiveGestures() != 2 {
		t.Fatalf("active = %d, want 2", a.ActiveGestures())
	}
}

func TestArbiterReleaseOnlyOwner(t *testing.T) {
	a := NewArbiter()
	res := testResource(1)
	a.Acquire(res, human, "g1", 0)
	// A different gesture's release must not free the holder.
	a.Release(res, human, "other")
	if got := a.Acquire(res, agent, "g2", 0); got != AcquireBusy {
		t.Fatalf("foreign release freed lock, got %v", got)
	}
	a.Release(res, human, "g1")
	if got := a.Acquire(res, agent, "g2", 0); got != AcquireHeld {
		t.Fatalf("after release = %v, want held", got)
	}
}

func TestArbiterTTLExpiry(t *testing.T) {
	clock := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	a := newArbiterWithClock(func() time.Time { return clock }, 10*time.Second)
	res := testResource(1)
	a.Acquire(res, human, "g1", time.Second)
	clock = clock.Add(2 * time.Second)
	if got := a.Acquire(res, agent, "g2", time.Second); got != AcquireHeld {
		t.Fatalf("after ttl = %v, want held", got)
	}
}

func TestArbiterEmergencyRelease(t *testing.T) {
	a := NewArbiter()
	a.Acquire(testResource(1), human, "g1", 0)
	a.Acquire(testResource(2), agent, "g2", 0)
	if n := a.EmergencyRelease(); n != 2 {
		t.Fatalf("emergency release cleared %d, want 2", n)
	}
	if a.ActiveGestures() != 0 {
		t.Fatalf("active after emergency = %d, want 0", a.ActiveGestures())
	}
}

func TestArbiterDifferentDisplaysDoNotContend(t *testing.T) {
	a := NewArbiter()
	first := testResource(1)
	second := first
	second.DisplayID = 2
	if got := a.Acquire(first, human, "g1", 0); got != AcquireHeld {
		t.Fatalf("display 1 = %v", got)
	}
	if got := a.Acquire(second, agent, "g2", 0); got != AcquireHeld {
		t.Fatalf("display 2 should be independent, got %v", got)
	}
}

func TestArbiterEmergencyReleaseRuntimeScoped(t *testing.T) {
	a := NewArbiter()
	sameRuntime := testResource(1)
	otherRuntime := testResource(1)
	otherRuntime.RuntimeID = "runtime-2"
	a.Acquire(sameRuntime, human, "g1", 0)
	a.Acquire(otherRuntime, agent, "g2", 0)
	if n := a.EmergencyReleaseRuntime(sameRuntime.WorkspaceID, sameRuntime.RuntimeID); n != 1 {
		t.Fatalf("runtime emergency release cleared %d, want 1", n)
	}
	if got := a.Acquire(sameRuntime, agent, "g3", 0); got != AcquireHeld {
		t.Fatal("runtime lock was not released")
	}
	if got := a.Acquire(otherRuntime, human, "g4", 0); got != AcquireBusy {
		t.Fatalf("other runtime lock was released, got %v", got)
	}
}
func TestArbiterInvalidInput(t *testing.T) {
	a := NewArbiter()
	bad := protocol.ResourceKey{WorkspaceID: "ws", RuntimeID: "rt", UID: 1} // missing canonical backend
	if got := a.Acquire(bad, human, "g1", 0); got != AcquireBusy {
		t.Fatalf("invalid resource = %v, want busy", got)
	}
	if got := a.Acquire(testResource(1), Principal{Kind: "ghost", ID: "x"}, "g1", 0); got != AcquireBusy {
		t.Fatalf("invalid principal = %v, want busy", got)
	}
	if got := a.Acquire(testResource(1), human, "", 0); got != AcquireBusy {
		t.Fatalf("empty gesture = %v, want busy", got)
	}
}
