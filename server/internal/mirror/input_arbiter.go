package mirror

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// PrincipalKind distinguishes the three contenders for a display resource.
type PrincipalKind string

const (
	// PrincipalHuman is a remote viewer injecting input through mirror-input.
	PrincipalHuman PrincipalKind = "human"
	// PrincipalAgent is a task acting through the managed virtual-screen path.
	PrincipalAgent PrincipalKind = "agent"
	// PrincipalLocal is the person physically at the host machine. Its input is
	// never blocked by remote locks; the kind is used for emergency accounting.
	PrincipalLocal PrincipalKind = "local"
)

// Principal identifies one contender. ID is the viewer id for humans and the
// task id for agents; it must be unique within a kind.
type Principal struct {
	Kind PrincipalKind
	ID   string
}

func (p Principal) valid() bool {
	if p.ID == "" {
		return false
	}
	switch p.Kind {
	case PrincipalHuman, PrincipalAgent, PrincipalLocal:
		return true
	default:
		return false
	}
}

// gestureLock is one atomic click/drag/type/key-chord hold on a resource.
type gestureLock struct {
	principal Principal
	gestureID string
	expiresAt time.Time
}

// DefaultGestureTTL bounds how long a single gesture may hold a resource
// before being forcibly released (dropped peer, lost pointer-up, stalled task).
const DefaultGestureTTL = 5 * time.Second

// Arbiter serializes atomic gestures per display resource with first-come,
// first-served semantics. It never queues: a failed Acquire returns busy and
// the contender retries after re-observing. Different resources never contend,
// so a human on a physical screen never blocks an agent on the virtual screen.
//
// Locks are lazily expired on access, so the arbiter needs no background
// goroutine for correctness. It is safe for concurrent use.
type Arbiter struct {
	mu      sync.Mutex
	locks   map[string]*gestureLock
	now     func() time.Time
	maxHold time.Duration
}

// NewArbiter constructs an arbiter using the wall clock.
func NewArbiter() *Arbiter {
	return &Arbiter{
		locks:   make(map[string]*gestureLock),
		now:     time.Now,
		maxHold: DefaultGestureTTL,
	}
}

func newArbiterWithClock(now func() time.Time, maxHold time.Duration) *Arbiter {
	if maxHold <= 0 {
		maxHold = DefaultGestureTTL
	}
	return &Arbiter{locks: make(map[string]*gestureLock), now: now, maxHold: maxHold}
}

func resourceKey(r protocol.ResourceKey) string {
	return r.BackendIdentity + "|" + r.WorkspaceID + "|" + r.RuntimeID + "|" +
		strconv.FormatUint(uint64(r.UID), 10) + "|" + strconv.FormatUint(uint64(r.DisplayID), 10)
}

// AcquireResult reports the outcome of an FCFS acquisition.
type AcquireResult string

const (
	AcquireHeld    AcquireResult = "held"    // the resource is now held for this gesture
	AcquireReentry AcquireResult = "reentry" // this gesture already held it; TTL renewed
	AcquireBusy    AcquireResult = "busy"    // another gesture currently owns the resource
)

// Acquire attempts to hold the resource for one atomic gesture. A repeat for
// the same principal+gesture renews the TTL (e.g. a drag continuation). A lock
// held by the same principal but a different gesture is still contention.
func (a *Arbiter) Acquire(resource protocol.ResourceKey, principal Principal, gestureID string, ttl time.Duration) AcquireResult {
	if resource.Validate() != nil || !principal.valid() || gestureID == "" {
		return AcquireBusy
	}
	if ttl <= 0 || ttl > a.maxHold {
		ttl = a.maxHold
	}
	key := resourceKey(resource)
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked(now)
	if existing, exists := a.locks[key]; exists {
		if existing.principal == principal && existing.gestureID == gestureID {
			existing.expiresAt = now.Add(ttl)
			return AcquireReentry
		}
		return AcquireBusy
	}
	a.locks[key] = &gestureLock{principal: principal, gestureID: gestureID, expiresAt: now.Add(ttl)}
	return AcquireHeld
}

// Release frees a gesture only if it still belongs to that principal/gesture.
func (a *Arbiter) Release(resource protocol.ResourceKey, principal Principal, gestureID string) {
	key := resourceKey(resource)
	a.mu.Lock()
	defer a.mu.Unlock()
	if lock, ok := a.locks[key]; ok &&
		lock.principal == principal && lock.gestureID == gestureID {
		delete(a.locks, key)
	}
}

// Sweep reaps expired locks. Acquisition already sweeps lazily; this is exposed
// for periodic janitors that want to keep the map bounded when idle.
func (a *Arbiter) Sweep() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked(a.now())
}

func (a *Arbiter) sweepLocked(now time.Time) {
	for k, lock := range a.locks {
		if !lock.expiresAt.After(now) {
			delete(a.locks, k)
		}
	}
}

// EmergencyRelease clears every remote (human/agent) gesture lock across all
// resources immediately. The local user and runtime owner can always halt all
// remote input regardless of FCFS ordering. It returns the number cleared.
func (a *Arbiter) EmergencyRelease() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for k, lock := range a.locks {
		if lock.principal.Kind != PrincipalLocal {
			delete(a.locks, k)
			n++
		}
	}
	return n
}

// EmergencyReleaseRuntime clears remote gesture locks for one runtime only.
func (a *Arbiter) EmergencyReleaseRuntime(workspaceID, runtimeID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for k, lock := range a.locks {
		parts := strings.Split(k, "|")
		if lock.principal.Kind != PrincipalLocal && len(parts) >= 3 &&
			parts[1] == workspaceID && parts[2] == runtimeID {
			delete(a.locks, k)
			n++
		}
	}
	return n
}

// ActiveGestures reports the number of currently held (non-expired) locks.
func (a *Arbiter) ActiveGestures() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked(a.now())
	return len(a.locks)
}

// Holder reports the principal and gesture currently holding a resource, or
// false when it is free. Used to fan out "who is controlling" to viewers.
func (a *Arbiter) Holder(resource protocol.ResourceKey) (Principal, string, bool) {
	key := resourceKey(resource)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked(a.now())
	if lock, ok := a.locks[key]; ok {
		return lock.principal, lock.gestureID, true
	}
	return Principal{}, "", false
}

// ReleasePrincipal frees every gesture currently held by one principal across
// all resources (used when a viewer's control grant ends or the input channel
// drops, so a lost peer can never pin a resource).
func (a *Arbiter) ReleasePrincipal(principal Principal) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for k, lock := range a.locks {
		if lock.principal == principal {
			delete(a.locks, k)
			n++
		}
	}
	return n
}
