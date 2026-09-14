package vscreen

import (
	"context"
	"fmt"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"sync"
	"time"
)

// Manager keeps one actor per backend/workspace/runtime/login for its lifetime.
type Manager struct {
	closed bool
	mu     sync.Mutex
	actors map[ResourceKey]*Actor
	driver Driver
	clock  Clock
}

func NewManager(driver Driver, clock Clock) *Manager {
	return &Manager{actors: make(map[ResourceKey]*Actor), driver: driver, clock: clock}
}

// For returns the shared runtime actor without creating a native display.
func (m *Manager) For(k ResourceKey) (*Actor, error) {
	if err := k.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, &Error{Reason: protocol.VscreenNativeUnavailable}
	}
	if a := m.actors[k]; a != nil {
		return a, nil
	}
	a := &Actor{key: k, driver: m.driver, clock: m.clock, observations: make(map[string]uint64), actions: make(map[string]actionEntry)}
	m.actors[k] = a
	return a, nil
}

// Run services expirations until shutdown; callers own and join this goroutine.
func (m *Manager) Run(ctx context.Context) error {
	defer m.revokeAll()
	ticker := time.NewTicker(LeaseHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			m.mu.Lock()
			actors := make([]*Actor, 0, len(m.actors))
			for _, a := range m.actors {
				actors = append(actors, a)
			}
			m.mu.Unlock()
			for _, a := range actors {
				if err := a.Sweep(ctx); err != nil {
					return err
				}
			}
		}
	}
}

// Actor serializes native operations while permitting immediate authority revocation.
type Actor struct {
	mu                      sync.Mutex
	nativeMu                sync.Mutex
	key                     ResourceKey
	driver                  Driver
	clock                   Clock
	display                 Display
	ready, frozen, stopping bool
	closed                  bool
	generation              uint64
	lease                   *Lease
	started                 time.Time
	queue                   []*waiter
	observations            map[string]uint64
	actions                 map[string]actionEntry
	sequence                uint64
	cancelAction            context.CancelFunc
}

// Status is a copy of current authority, never a grant.
type Status struct {
	Display                 Display
	Ready, Frozen, Stopping bool
	Lease                   Lease
	Waiting                 int
}

func (a *Actor) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := Status{Display: a.display, Ready: a.ready, Frozen: a.frozen, Stopping: a.stopping, Waiting: len(a.queue)}
	if a.lease != nil {
		s.Lease = *a.lease
	}
	return s
}

// Ensure deduplicates native creation and never ties its lifetime to viewer count.
func (a *Actor) Ensure(ctx context.Context) (Display, error) {
	a.nativeMu.Lock()
	defer a.nativeMu.Unlock()
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return Display{}, &Error{Reason: protocol.VscreenNativeUnavailable}
	}
	if a.ready {
		d := a.display
		a.mu.Unlock()
		return d, nil
	}
	generation := a.generation
	a.mu.Unlock()
	d, err := a.driver.Ensure(ctx, a.key)
	if err != nil {
		return Display{}, err
	}
	if d.Resource != a.key || d.DisplayID == 0 {
		return Display{}, fmt.Errorf("%w: native display identity", protocol.ErrInvalidVscreenContract)
	}
	if err = d.Epoch.Validate(); err != nil {
		return Display{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if generation != a.generation {
		return Display{}, &Error{Reason: protocol.VscreenStaleSnapshot}
	}
	a.display = d
	a.ready = true
	return d, nil
}

// Reconcile invalidates all authority on native/display/geometry changes. Explicit
// recovery is required even after a new native host has been created successfully.
func (a *Actor) Reconcile(d Display) error {
	if d.Resource != a.key || d.DisplayID == 0 {
		return &Error{Reason: protocol.VscreenSourceGone}
	}
	if err := d.Epoch.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return &Error{Reason: protocol.VscreenNativeUnavailable}
	}
	if a.display != d {
		a.freezeLocked()
		a.display = d
		a.ready = true
		a.observations = make(map[string]uint64)
	}
	return nil
}
func (a *Actor) freezeLocked() {
	a.frozen = true
	a.generation++
	if a.lease != nil {
		a.lease.Cancelled = true
	}
	if a.cancelAction != nil {
		a.cancelAction()
	}
	for _, w := range a.queue {
		w.err = &Error{Reason: protocol.VscreenActionUncertainReason}
		close(w.done)
	}
	a.queue = nil
}

// Recover is a trusted lifecycle seam after explicit intervention, never an automatic retry.
func (a *Actor) Recover(ctx context.Context, epoch Epoch) error {
	a.nativeMu.Lock()
	defer a.nativeMu.Unlock()
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return &Error{Reason: protocol.VscreenNativeUnavailable}
	}
	if err := a.driver.Quiesce(ctx, a.key); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return &Error{Reason: protocol.VscreenNativeUnavailable}
	}
	if epoch != a.display.Epoch {
		return &Error{Reason: protocol.VscreenStaleSnapshot}
	}
	a.lease = nil
	a.frozen = false
	a.stopping = false
	a.actions = make(map[string]actionEntry)
	a.observations = make(map[string]uint64)
	return nil
}

// Dispose stops authority before native cleanup; failure preserves the frozen gate.
func (a *Actor) Dispose(ctx context.Context) error {
	a.mu.Lock()
	a.freezeLocked()
	a.stopping = true
	a.mu.Unlock()
	a.nativeMu.Lock()
	defer a.nativeMu.Unlock()
	if err := a.driver.Quiesce(ctx, a.key); err != nil {
		return err
	}
	if err := a.driver.Dispose(ctx, a.key); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ready = false
	a.lease = nil
	return nil
}
