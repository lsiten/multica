package appcontrol

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/appclaim"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const operationLimit = 3 * time.Second

type backend interface {
	call(context.Context, string, any, any) error
	close() error
}
type ownedWindow struct {
	window         Window
	display        Display
	claim          *appclaim.Lock
	human          bool
	nativeReleased bool
}

// Controller serializes native work and retains process claims until a native quiescence barrier.
type Controller struct {
	activeKey    protocol.ResourceKey
	activeCancel context.CancelFunc
	config       Config
	backend      backend
	gate         chan struct{}
	mu           sync.Mutex
	closed       bool
	frozen       map[protocol.ResourceKey]bool
	windows      map[string]*ownedWindow
	candidates   map[string]candidateRecord
	actions      map[actionIdentity]actionRecord
	sequence     map[grantBinding]uint64
}

// New constructs the native controller; it never requests permissions or launches an app.
func New(config Config) (*Controller, error) {
	if config.Authorize == nil || config.AuthorizeHuman == nil {
		return nil, refusal("authority_required")
	}
	b, err := newBackend()
	if err != nil {
		return nil, err
	}
	return controller(config, b), nil
}
func controller(config Config, b backend) *Controller {
	c := &Controller{config: config, backend: b, gate: make(chan struct{}, 1), frozen: make(map[protocol.ResourceKey]bool), windows: make(map[string]*ownedWindow), actions: make(map[actionIdentity]actionRecord), sequence: make(map[grantBinding]uint64)}
	c.gate <- struct{}{}
	return c
}
func (c *Controller) enter(ctx context.Context, key protocol.ResourceKey) (context.Context, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, operationLimit)
	select {
	case <-ctx.Done():
		cancel()
		return nil, nil, ctx.Err()
	case <-c.gate:
	}
	c.mu.Lock()
	closed := c.closed
	if !closed {
		c.activeKey = key
		c.activeCancel = cancel
	}
	c.mu.Unlock()
	if closed {
		c.gate <- struct{}{}
		cancel()
		return nil, nil, refusal("closed")
	}
	return ctx, func() { c.mu.Lock(); c.activeCancel = nil; c.mu.Unlock(); c.gate <- struct{}{}; cancel() }, nil
}
func (c *Controller) authorize(ctx context.Context, a Authority, access Access) (Display, error) {
	if a.Resource.Validate() != nil || a.Epoch.Validate() != nil {
		return Display{}, refusal("stale_authority")
	}
	if access == ControlAccess && (a.TaskID == "" || a.TransactionID == "" || a.LeaseEpoch == 0) {
		return Display{}, refusal("stale_authority")
	}
	d, err := c.config.Authorize(ctx, a, access)
	if err != nil {
		return Display{}, err
	}
	if d.Resource != a.Resource || d.Epoch != a.Epoch || !d.Virtual || d.ID == 0 || !d.Bounds.valid() {
		return Display{}, refusal("stale_authority")
	}
	c.mu.Lock()
	blocked := c.closed || access == ControlAccess && c.frozen[a.Resource]
	c.mu.Unlock()
	if blocked {
		return Display{}, refusal("needs_intervention")
	}
	return d, nil
}
func (c *Controller) freeze(key protocol.ResourceKey) {
	c.mu.Lock()
	c.frozen[key] = true
	if c.activeKey == key && c.activeCancel != nil {
		c.activeCancel()
	}
	c.mu.Unlock()
}
func (c *Controller) owned(handle string, d Display) (*ownedWindow, error) {
	w := c.windows[handle]
	if w == nil || w.display != d || w.human {
		return nil, refusal("stale_window")
	}
	return w, nil
}
func nativeClaim(p Process) (*appclaim.Lock, error) {
	if p.PID <= 0 || p.Start == "" || p.UID != uint32(os.Getuid()) {
		return nil, refusal("process_changed")
	}
	account, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		return nil, err
	}
	return appclaim.Acquire(filepath.Join(account.HomeDir, ".multica", "native-claims"), appclaim.Key{UID: p.UID, PID: p.PID, ProcessStartIdentity: p.Start})
}

// Launch opens a new installed app nonactively and moves only its newly identified owned window.
func (c *Controller) Launch(ctx context.Context, a Authority, request LaunchRequest) (Window, error) {
	if request.BundleID == "" || len(request.BundleID) > 255 || len(request.Files) > 16 {
		return Window{}, refusal("invalid_launch")
	}
	for _, path := range request.Files {
		if !filepath.IsAbs(path) {
			return Window{}, refusal("invalid_launch")
		}
	}
	ctx, leave, err := c.enter(ctx, a.Resource)
	if err != nil {
		return Window{}, err
	}
	defer leave()
	d, err := c.authorize(ctx, a, ControlAccess)
	if err != nil {
		return Window{}, err
	}
	if len(c.windows) >= 128 {
		return Window{}, refusal("needs_intervention")
	}
	var w Window
	if err = c.backend.call(ctx, "launch", request, &w); err != nil {
		c.freeze(a.Resource)
		return Window{}, err
	}
	if w.Handle == "" || w.WindowID == 0 || !w.Bounds.valid() || w.Process.BundleID != request.BundleID {
		return Window{}, refusal("stale_window")
	}
	claim, err := nativeClaim(w.Process)
	if err != nil {
		return Window{}, refusal("app_claim_conflict")
	}
	owned := &ownedWindow{window: w, display: d, claim: claim}
	c.windows[w.Handle] = owned
	// A launch completion can arrive after cancellation; no move follows a revoked authority.
	if _, err = c.authorize(ctx, a, ControlAccess); err != nil {
		c.freeze(a.Resource)
		return w, err
	}
	var moved Window
	err = c.backend.call(ctx, "move", map[string]any{"Window": w, "Display": d, "Background": true}, &moved)
	if err != nil {
		c.freeze(a.Resource)
		return w, err
	}
	if !validWindowReply(moved, w, d) {
		c.freeze(a.Resource)
		return w, refusal("stale_window")
	}
	owned.window = moved
	return owned.window, nil
}

// Quiesce closes authority immediately and returns only after all native callbacks finish.
func (c *Controller) Quiesce(ctx context.Context, key protocol.ResourceKey) error {
	c.freeze(key)
	ctx, leave, err := c.enter(ctx, key)
	if err != nil {
		return err
	}
	defer leave()
	return c.backend.call(ctx, "quiesce", map[string]any{"Resource": key}, nil)
}

func validWindowReply(actual, expected Window, display Display) bool {
	return actual.Handle == expected.Handle && actual.Process == expected.Process && actual.WindowID == expected.WindowID && actual.DisplayID == display.ID && display.Bounds.Contains(actual.Bounds)
}
