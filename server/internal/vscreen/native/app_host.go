package native

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type appController interface {
	ListWindows(context.Context, appcontrol.HumanRequest) (appcontrol.WindowCandidates, error)
	AdoptWindow(context.Context, appcontrol.HumanRequest) (appcontrol.Window, error)
	ListApps(context.Context, appcontrol.Authority) (appcontrol.AppList, error)
	Launch(context.Context, appcontrol.Authority, appcontrol.LaunchRequest) (appcontrol.Window, error)
	Observe(context.Context, appcontrol.Authority, string, bool) (appcontrol.Observation, error)
	Act(context.Context, protocol.VscreenActionRequest) (appcontrol.Result, error)
	Quiesce(context.Context, protocol.ResourceKey) error
	Resume(context.Context, appcontrol.Authority) error
	Dispose(context.Context, protocol.ResourceKey) error
	HumanTransfer(context.Context, appcontrol.HumanRequest) (appcontrol.Window, error)
	Close(context.Context) error
}
type appLease struct {
	resumed          uint64
	authority        appcontrol.Authority
	expires          time.Time
	high             uint64
	ready, quiescent bool
	timer            *time.Timer
	observer         string
	observerExpiry   time.Time
	human            *HumanGrant
	humanExpiry      time.Time
}
type appFlight struct {
	authority appcontrol.Authority
	key       protocol.ResourceKey
	cancel    context.CancelFunc
}
type appHost struct {
	readDisplay   func(uint32) (Display, error)
	cleanupErr    error
	snapshotUsed  map[string]bool
	humanUsed     map[string]bool
	mu            sync.Mutex
	writeMu       sync.Mutex
	conn          net.Conn
	build, epoch  string
	controller    appController
	controllerErr error
	resources     map[protocol.ResourceKey]resourceDisplay
	catalog       sourceCatalog
	leases        map[protocol.ResourceKey]*appLease
	flights       map[string]appFlight
	lastID        uint64
	closed        bool
	wg            sync.WaitGroup
	done          chan struct{}
	captures      *captureHost
}

func newAppHost(conn net.Conn, build, epoch string, captures *captureHost) *appHost {
	h := &appHost{conn: conn, build: build, epoch: epoch, captures: captures, resources: make(map[protocol.ResourceKey]resourceDisplay), catalog: make(sourceCatalog), leases: make(map[protocol.ResourceKey]*appLease), flights: make(map[string]appFlight), done: make(chan struct{}), snapshotUsed: make(map[string]bool), humanUsed: make(map[string]bool)}
	h.readDisplay = describeDisplay
	h.controller, h.controllerErr = appcontrol.New(appcontrol.Config{Authorize: h.authorize, AuthorizeHuman: h.authorizeHuman})
	return h
}
func appRefusal(code string) error { return &appcontrol.Error{Reason: code} }
func (h *appHost) syncRegistry(resources map[protocol.ResourceKey]resourceDisplay, catalog sourceCatalog) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.resources = make(map[protocol.ResourceKey]resourceDisplay, len(resources))
	for k, v := range resources {
		h.resources[k] = v
	}
	h.catalog = make(sourceCatalog, len(catalog))
	for k, v := range catalog {
		h.catalog[k] = v
	}
}
func appDisplay(key protocol.ResourceKey, r resourceDisplay) appcontrol.Display {
	return appcontrol.Display{Resource: key, Epoch: r.epoch, ID: r.display.ID, Virtual: true, Bounds: appcontrol.Bounds{X: float64(r.display.X), Y: float64(r.display.Y), Width: r.display.LogicalWidth, Height: r.display.LogicalHeight}}
}
func (h *appHost) validLocked(a appcontrol.Authority, access appcontrol.Access) (resourceDisplay, error) {
	r, ok := h.resources[a.Resource]
	if h.closed || !ok || r.display.ID == 0 || a.Epoch != r.epoch || a.Resource.Validate() != nil {
		return r, appRefusal("stale_authority")
	}
	l := h.leases[a.Resource]
	if l == nil {
		return r, appRefusal("stale_authority")
	}
	now := time.Now()
	if access == appcontrol.ObserveAccess && a.ObserverGrant != "" && a.ObserverGrant == l.observer && now.Before(l.observerExpiry) {
		return r, nil
	}
	if a.ObserverGrant != "" || a.TaskID == "" || a.TransactionID == "" || a.LeaseEpoch == 0 || a != l.authority || !now.Before(l.expires) {
		return r, appRefusal("stale_authority")
	}
	return r, nil
}
func (h *appHost) authorize(ctx context.Context, a appcontrol.Authority, access appcontrol.Access) (appcontrol.Display, error) {
	if err := ctx.Err(); err != nil {
		return appcontrol.Display{}, err
	}
	h.mu.Lock()
	r, err := h.validLocked(a, access)
	h.mu.Unlock()
	if err != nil {
		return appcontrol.Display{}, err
	}
	live, err := h.readDisplay(r.display.ID)
	if err != nil || live.UUID != r.display.UUID || geometryChanged(r.display, live) {
		return appcontrol.Display{}, appRefusal("stale_authority")
	}
	h.mu.Lock()
	_, err = h.validLocked(a, access)
	h.mu.Unlock()
	if err != nil {
		return appcontrol.Display{}, err
	}
	return appDisplay(a.Resource, r), ctx.Err()
}
func (h *appHost) fenceLocked(key protocol.ResourceKey) {
	if l := h.leases[key]; l != nil {
		l.expires = time.Time{}
		l.ready = false
		l.quiescent = false
		if l.timer != nil {
			l.timer.Stop()
		}
	}
	for _, f := range h.flights {
		if f.key == key {
			f.cancel()
		}
	}
}
func (h *appHost) grant(request Request) error {
	a := request.App.Authority
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.resources[a.Resource]
	if h.closed || !ok || r.display.ID == 0 || r.epoch != a.Epoch || request.Resource != a.Resource || request.Epoch != a.Epoch || request.App.LeaseTTLMS == 0 || request.App.LeaseTTLMS > 15000 {
		return appRefusal("stale_authority")
	}
	l := h.leases[a.Resource]
	if l == nil {
		if len(h.leases) >= 4096 {
			return appRefusal("app_limit")
		}
		l = &appLease{quiescent: true}
		h.leases[a.Resource] = l
	}
	expiry := time.Now().Add(time.Duration(request.App.LeaseTTLMS) * time.Millisecond)
	if request.Operation == "app_observer_grant" {
		if len(a.ObserverGrant) < 32 || len(a.ObserverGrant) > 128 || a.TaskID != "" || a.TransactionID != "" || a.LeaseEpoch != 0 {
			return appRefusal("stale_authority")
		}
		l.observer = a.ObserverGrant
		l.observerExpiry = expiry
		return nil
	}
	if a.ObserverGrant != "" || a.TaskID == "" || len(a.TaskID) > 256 || a.TransactionID == "" || len(a.TransactionID) > 256 || a.LeaseEpoch == 0 {
		return appRefusal("stale_authority")
	}
	if request.Operation == "app_renew" {
		if l.authority != a || !time.Now().Before(l.expires) {
			return appRefusal("stale_authority")
		}
	} else if a.LeaseEpoch <= l.high || !l.quiescent {
		return appRefusal("quiescence_required")
	}
	if l.timer != nil {
		l.timer.Stop()
	}
	l.authority = a
	l.high = a.LeaseEpoch
	l.expires = expiry
	l.timer = time.AfterFunc(time.Until(expiry), func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if l.authority == a && l.expires.Equal(expiry) && !time.Now().Before(l.expires) {
			h.fenceLocked(a.Resource)
		}
	})
	return nil
}
func (h *appHost) authorizeHuman(ctx context.Context, r appcontrol.HumanRequest) (appcontrol.Display, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.leases[r.Resource]
	owned, ok := h.resources[r.Resource]
	if ctx.Err() != nil || h.closed || !ok || l == nil || !l.quiescent || l.ready || l.human == nil || !time.Now().Before(l.humanExpiry) {
		return appcontrol.Display{}, appRefusal("human_grant_required")
	}
	g := *l.human
	// Consume before native movement; uncertain operations cannot replay the capability.
	l.human = nil
	if g.Capability != r.Grant || g.WindowHandle != r.WindowHandle || g.Direction != r.Direction || (r.Direction == "list_existing" || r.Direction == "adopt_existing") && g.InterventionID != r.InterventionID {
		return appcontrol.Display{}, appRefusal("human_grant_required")
	}
	if r.Direction == "to_virtual" || r.Direction == "list_existing" || r.Direction == "adopt_existing" {
		live, err := h.readDisplay(owned.display.ID)
		if err != nil || live.UUID != owned.display.UUID || geometryChanged(owned.display, live) {
			return appcontrol.Display{}, appRefusal("human_grant_required")
		}
		return appDisplay(r.Resource, owned), nil
	}
	for _, record := range h.catalog {
		d := record.display
		if "display:"+d.UUID != g.DestinationSourceID || d.Managed {
			continue
		}
		for _, resource := range h.resources {
			if resource.display.ID == d.ID {
				return appcontrol.Display{}, appRefusal("human_grant_required")
			}
		}
		live, err := h.readDisplay(d.ID)
		if err != nil || live.UUID != d.UUID || geometryChanged(d, live) {
			return appcontrol.Display{}, appRefusal("human_grant_required")
		}
		return appcontrol.Display{Resource: r.Resource, Epoch: record.epoch, ID: d.ID, Bounds: appcontrol.Bounds{X: float64(d.X), Y: float64(d.Y), Width: d.LogicalWidth, Height: d.LogicalHeight}}, nil
	}
	return appcontrol.Display{}, appRefusal("human_grant_required")
}
func (h *appHost) issueHuman(r Request) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.leases[r.Resource]
	resource, ok := h.resources[r.Resource]
	g := r.App.Human
	if h.closed || !ok || resource.epoch != r.Epoch || l == nil || !l.quiescent || g == nil || len(g.Capability) < 32 || len(g.Capability) > 128 || g.InterventionID == "" || len(g.InterventionID) > 256 || (g.WindowHandle == "" && g.Direction != "list_existing") || (g.WindowHandle != "" && g.Direction == "list_existing") || len(g.WindowHandle) > 256 || g.Direction != "to_real" && g.Direction != "to_virtual" && g.Direction != "list_existing" && g.Direction != "adopt_existing" || r.App.LeaseTTLMS == 0 || r.App.LeaseTTLMS > 15000 {
		return appRefusal("human_grant_required")
	}
	if h.humanUsed[g.Capability] || len(h.humanUsed) >= 4096 {
		return appRefusal("human_grant_required")
	}
	h.humanUsed[g.Capability] = true
	copyGrant := *g
	l.human = &copyGrant
	l.humanExpiry = time.Now().Add(time.Duration(r.App.LeaseTTLMS) * time.Millisecond)
	return nil
}
func appError(err error) string {
	if err == nil {
		return ""
	}
	var e *appcontrol.Error
	if errors.As(err, &e) {
		switch e.Reason {
		case "unsupported_platform", "stale_authority", "stale_window", "stale_snapshot", "needs_intervention", "action_uncertain", "action_conflict", "invalid_action", "invalid_launch", "app_claim_conflict", "process_changed", "authority_required", "human_grant_required", "accessibility_denied", "screen_recording_denied", "quiescence_required", "app_limit":
			return e.Reason
		}
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	return "app_unavailable"
}
func (h *appHost) execute(ctx context.Context, r Request) (out *AppResponse, err error) {
	out = &AppResponse{}
	if h.controllerErr != nil {
		return out, h.controllerErr
	}
	a := r.App.Authority
	switch r.Operation {
	case "app_probe":
		p, e := appcontrol.ProbePermissions(ctx)
		out.Permissions = &p
		return out, e
	case "app_quiesce", "app_revoke", "app_dispose":
		if r.Operation == "app_dispose" {
			err = h.controller.Dispose(ctx, r.Resource)
		} else {
			err = h.controller.Quiesce(ctx, r.Resource)
		}
		h.mu.Lock()
		if l := h.leases[r.Resource]; l != nil && err == nil {
			l.quiescent = true
		}
		h.mu.Unlock()
		return out, err
	case "app_resume":
		h.mu.Lock()
		lease := h.leases[r.Resource]
		if lease == nil || !lease.quiescent || lease.resumed >= a.LeaseEpoch || lease.authority != a {
			h.mu.Unlock()
			return out, appRefusal("quiescence_required")
		}
		lease.resumed = a.LeaseEpoch
		h.mu.Unlock()
		err = h.controller.Resume(ctx, a)
		h.mu.Lock()
		if l := h.leases[r.Resource]; l != nil && l.authority == a && time.Now().Before(l.expires) && err == nil {
			l.ready = true
			l.quiescent = false
		}
		h.mu.Unlock()
		return out, err
	case "app_human_candidates", "app_human_adopt":
		if r.App.Human == nil {
			return out, appRefusal("human_grant_required")
		}
		g := r.App.Human
		request := appcontrol.HumanRequest{Grant: g.Capability, Resource: r.Resource, WindowHandle: g.WindowHandle, Direction: g.Direction, InterventionID: g.InterventionID}
		if r.Operation == "app_human_candidates" {
			v, e := h.controller.ListWindows(ctx, request)
			out.Candidates = &v
			return out, e
		}
		w, e := h.controller.AdoptWindow(ctx, request)
		out.Window = &w
		return out, e
	case "app_human_transfer":
		if r.App.Human == nil {
			return out, appRefusal("human_grant_required")
		}
		g := r.App.Human
		w, e := h.controller.HumanTransfer(ctx, appcontrol.HumanRequest{Grant: g.Capability, Resource: r.Resource, WindowHandle: g.WindowHandle, Direction: g.Direction})
		out.Window = &w
		return out, e
	}
	h.mu.Lock()
	_, err = h.validLocked(a, appcontrol.ControlAccess)
	l := h.leases[a.Resource]
	ready := err == nil && l.ready
	if r.Operation == "app_observe" {
		_, err = h.validLocked(a, appcontrol.ObserveAccess)
		ready = err == nil
	}
	h.mu.Unlock()
	if !ready {
		return out, appRefusal("stale_authority")
	}
	switch r.Operation {
	case "app_list":
		apps, e := h.controller.ListApps(ctx, a)
		out.Apps = &apps
		err = e
	case "app_launch":
		if r.App.Launch == nil {
			return out, ErrProtocol
		}
		w, e := h.controller.Launch(ctx, a, *r.App.Launch)
		out.Window = &w
		err = e
	case "app_action":
		if r.App.Action == nil {
			return out, ErrProtocol
		}
		t := r.App.Action.Target
		if t.Resource != a.Resource || t.Epoch != a.Epoch || t.TaskID != a.TaskID || t.TransactionID != a.TransactionID || t.LeaseEpoch != a.LeaseEpoch {
			return out, appRefusal("stale_authority")
		}
		result, e := h.controller.Act(ctx, *r.App.Action)
		out.Result = &result
		err = e
	case "app_observe":
		observation, e := h.controller.Observe(ctx, a, r.App.WindowHandle, r.App.IncludePNG)
		if e != nil {
			return out, e
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		h.mu.Lock()
		_, err = h.validLocked(a, appcontrol.ObserveAccess)
		h.mu.Unlock()
		if err != nil {
			return out, err
		}
		png := observation.PNG
		observation.PNG = nil
		boundObservation(&observation)
		out.Observation = &observation
		if r.App.IncludePNG {
			if len(png) == 0 || len(png) > MaxMediaPayloadBytes || !validSnapshotID(r.App.SnapshotID) {
				return out, ErrProtocol
			}
			sum := sha256.Sum256(png)
			out.Snapshot = &SnapshotDescriptor{ID: r.App.SnapshotID, Resource: r.Resource, Epoch: r.Epoch, WindowHandle: r.App.WindowHandle, SnapshotRevision: observation.Window.SnapshotRevision, DisplayID: observation.Display.ID, Size: uint32(len(png)), SHA256: hex.EncodeToString(sum[:])}
			for offset := 0; offset < len(png); offset += SnapshotChunkBytes {
				if e := ctx.Err(); e != nil {
					return out, e
				}
				end := min(offset+SnapshotChunkBytes, len(png))
				if e := h.captures.writeSample(MediaSample{Kind: MediaSnapshot, StreamID: r.App.SnapshotID, Epoch: r.Epoch, DisplayID: observation.Display.ID, SnapshotOffset: uint32(offset), SnapshotTotal: uint32(len(png)), PNG: png[offset:end]}); e != nil {
					return out, e
				}
			}
		}
	default:
		return out, ErrProtocol
	}
	if err != nil {
		h.mu.Lock()
		h.fenceLocked(r.Resource)
		h.mu.Unlock()
	}
	return out, err
}
func validSnapshotID(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 16 && id == strings.ToLower(id)
}
func boundObservation(o *appcontrol.Observation) {
	if len(o.Elements) > 128 {
		o.Elements = o.Elements[:128]
		o.Truncated = true
	}
	for i := range o.Elements {
		e := &o.Elements[i]
		for _, s := range []*string{&e.Role, &e.Title, &e.Value} {
			if len(*s) > 512 {
				*s = string([]rune(*s)[:min(128, len([]rune(*s)))])
				o.Truncated = true
			}
		}
	}
	// JSON escaping can expand text six-fold, so enforce the encoded budget too.
	for len(o.Elements) > 0 {
		b, err := json.Marshal(o)
		if err == nil && len(b) <= 40*1024 {
			break
		}
		o.Elements = o.Elements[:len(o.Elements)-1]
		o.Truncated = true
	}
}
