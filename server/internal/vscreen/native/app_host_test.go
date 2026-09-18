package native

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type fakeAppController struct {
	started   chan struct{}
	cancelled chan struct{}
	closes    atomic.Int32
	uncertain bool
}

func (f *fakeAppController) Launch(ctx context.Context, a appcontrol.Authority, r appcontrol.LaunchRequest) (appcontrol.Window, error) {
	if r.BundleID == "blocked" {
		close(f.started)
		<-ctx.Done()
		close(f.cancelled)
		return appcontrol.Window{}, ctx.Err()
	}
	return appcontrol.Window{Handle: a.Resource.RuntimeID}, nil
}
func (f *fakeAppController) Observe(context.Context, appcontrol.Authority, string, bool) (appcontrol.Observation, error) {
	return appcontrol.Observation{}, nil
}
func (f *fakeAppController) Act(context.Context, protocol.VscreenActionRequest) (appcontrol.Result, error) {
	return appcontrol.Result{}, nil
}
func (f *fakeAppController) Quiesce(context.Context, protocol.ResourceKey) error {
	if f.uncertain {
		return appRefusal("action_uncertain")
	}
	return nil
}
func (f *fakeAppController) Resume(context.Context, appcontrol.Authority) error  { return nil }
func (f *fakeAppController) Dispose(context.Context, protocol.ResourceKey) error { return nil }
func (f *fakeAppController) HumanTransfer(context.Context, appcontrol.HumanRequest) (appcontrol.Window, error) {
	return appcontrol.Window{}, nil
}
func (f *fakeAppController) Close(context.Context) error { f.closes.Add(1); return nil }
func appFixture(t *testing.T) (*appHost, appcontrol.Authority, *fakeAppController) {
	t.Helper()
	a := appcontrol.Authority{Resource: protocol.ResourceKey{BackendIdentity: "https://fixture.invalid", WorkspaceID: "workspace", RuntimeID: "one", UID: uint32(os.Getuid())}, Epoch: protocol.VscreenEpoch{NativeEpoch: strings.Repeat("a", 64), DisplayGeneration: strings.Repeat("b", 64), GeometryRevision: 1}, TaskID: "task", TransactionID: "transaction", LeaseEpoch: 1}
	f := &fakeAppController{started: make(chan struct{}), cancelled: make(chan struct{})}
	h := &appHost{epoch: a.Epoch.NativeEpoch, build: "test", controller: f, resources: map[protocol.ResourceKey]resourceDisplay{a.Resource: {display: Display{ID: 1, UUID: "virtual", LogicalWidth: 800, LogicalHeight: 600}, epoch: a.Epoch}}, catalog: make(sourceCatalog), leases: make(map[protocol.ResourceKey]*appLease), flights: make(map[string]appFlight), snapshotUsed: make(map[string]bool), humanUsed: make(map[string]bool), done: make(chan struct{})}
	h.readDisplay = func(id uint32) (Display, error) {
		for _, r := range h.resources {
			if r.display.ID == id {
				return r.display, nil
			}
		}
		if r, ok := h.catalog[id]; ok {
			return r.display, nil
		}
		return Display{}, ErrUnavailable
	}
	t.Cleanup(func() {
		h.mu.Lock()
		for _, l := range h.leases {
			if l.timer != nil {
				l.timer.Stop()
			}
		}
		h.mu.Unlock()
	})
	return h, a, f
}
func appReq(op string, a appcontrol.Authority) Request {
	return Request{Version: 1, Build: "test", Operation: op, Resource: a.Resource, Epoch: a.Epoch, App: &AppRequest{Authority: a, LeaseTTLMS: 15000}}
}
func TestAppLeaseBoundariesObserverAndRecovery(t *testing.T) {
	h, a, _ := appFixture(t)
	r := appReq("app_grant", a)
	r.App.LeaseTTLMS = 15001
	if h.grant(r) == nil {
		t.Fatal("accepted overlong grant")
	}
	r.App.LeaseTTLMS = 15000
	bad := r
	bad.Resource.RuntimeID = "foreign"
	if h.grant(bad) == nil {
		t.Fatal("cross-resource grant")
	}
	if err := h.grant(r); err != nil {
		t.Fatal(err)
	}
	if h.grant(r) == nil {
		t.Fatal("replayed lease epoch")
	}
	if _, err := h.execute(t.Context(), appReq("app_resume", a)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.execute(t.Context(), appReq("app_resume", a)); err == nil {
		t.Fatal("same lease resumed twice")
	}
	next := a
	next.LeaseEpoch++
	if h.grant(appReq("app_grant", next)) == nil {
		t.Fatal("replaced running owner")
	}
	observer := a
	observer.TaskID = ""
	observer.TransactionID = ""
	observer.LeaseEpoch = 0
	observer.ObserverGrant = strings.Repeat("o", 32)
	if err := h.grant(appReq("app_observer_grant", observer)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.validLocked(observer, appcontrol.ObserveAccess); err != nil {
		t.Fatal(err)
	}
	if _, err := h.validLocked(observer, appcontrol.ControlAccess); err == nil {
		t.Fatal("observer mutated")
	}
	h.mu.Lock()
	h.fenceLocked(a.Resource)
	h.mu.Unlock()
	if h.grant(appReq("app_grant", next)) == nil {
		t.Fatal("grant before barrier")
	}
	if _, err := h.execute(t.Context(), appReq("app_quiesce", a)); err != nil {
		t.Fatal(err)
	}
	if err := h.grant(appReq("app_grant", next)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.validLocked(a, appcontrol.ControlAccess); err == nil {
		t.Fatal("old epoch accepted")
	}
}
func TestAppExpiryCancelsOnlyMatchingResource(t *testing.T) {
	h, a, _ := appFixture(t)
	r := appReq("app_grant", a)
	r.App.LeaseTTLMS = 20
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	other, stop := context.WithCancel(t.Context())
	defer stop()
	h.flights["one"] = appFlight{key: a.Resource, cancel: cancel}
	key := a.Resource
	key.RuntimeID = "two"
	h.flights["two"] = appFlight{key: key, cancel: stop}
	if err := h.grant(r); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("expiry did not fence in-flight work")
	}
	if other.Err() != nil {
		t.Fatal("sibling cancelled")
	}
	if h.grant(appReq("app_renew", a)) == nil {
		t.Fatal("expired lease renewed")
	}
}
func TestAppRevokeInflightAndSiblingOverMux(t *testing.T) {
	h, a, f := appFixture(t)
	parent, child := net.Pipe()
	h.conn = child
	go h.serve()
	defer parent.Close()
	sibling := a
	sibling.Resource.RuntimeID = "two"
	h.mu.Lock()
	h.resources[sibling.Resource] = resourceDisplay{display: Display{ID: 2, LogicalWidth: 800, LogicalHeight: 600}, epoch: a.Epoch}
	h.mu.Unlock()
	var id int
	send := func(r Request) string {
		t.Helper()
		id++
		r.ID = strconv.Itoa(id)
		if err := WriteMessage(parent, r); err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	read := func() Response {
		t.Helper()
		parent.SetReadDeadline(time.Now().Add(time.Second))
		var r Response
		if err := ReadMessage(parent, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, authority := range []appcontrol.Authority{a, sibling} {
		send(appReq("app_grant", authority))
		if r := read(); r.Error != "" {
			t.Fatal(r.Error)
		}
		send(appReq("app_resume", authority))
		if r := read(); r.Error != "" {
			t.Fatal(r.Error)
		}
	}
	blocked := appReq("app_launch", a)
	blocked.App.Launch = &appcontrol.LaunchRequest{BundleID: "blocked"}
	blockedID := send(blocked)
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("native operation not entered")
	}
	revokeID := send(appReq("app_revoke", a))
	replies := map[string]Response{}
	for range 2 {
		r := read()
		replies[r.ID] = r
	}
	if replies[blockedID].Error != "cancelled" || replies[revokeID].Error != "" {
		t.Fatalf("revoke results %+v", replies)
	}
	select {
	case <-f.cancelled:
	default:
		t.Fatal("native cancellation not observed")
	}
	launch := appReq("app_launch", sibling)
	launch.App.Launch = &appcontrol.LaunchRequest{BundleID: "sibling"}
	send(launch)
	if r := read(); r.Error != "" || r.App.Window.Handle != "two" {
		t.Fatalf("sibling failed %+v", r)
	}
	parent.Close()
	select {
	case <-h.done:
	case <-time.After(time.Second):
		t.Fatal("EOF did not join cleanup")
	}
	if f.closes.Load() != 1 {
		t.Fatal("EOF did not restore controller")
	}
}
func TestAppHumanGrantIsSeparateConsumedAndCatalogScoped(t *testing.T) {
	h, a, _ := appFixture(t)
	if err := h.grant(appReq("app_grant", a)); err != nil {
		t.Fatal(err)
	}
	h.catalog[9] = sourceRecord{display: Display{ID: 9, UUID: "physical", LogicalWidth: 800, LogicalHeight: 600}, epoch: a.Epoch}
	req := appReq("app_human_grant", a)
	req.App.Human = &HumanGrant{Capability: strings.Repeat("c", 32), InterventionID: "intervention", WindowHandle: "window", Direction: "to_real", DestinationSourceID: "display:physical"}
	if err := h.issueHuman(req); err != nil {
		t.Fatal(err)
	}
	human := appcontrol.HumanRequest{Resource: a.Resource, Grant: req.App.Human.Capability, WindowHandle: "window", Direction: "to_real"}
	d, err := h.authorizeHuman(t.Context(), human)
	if err != nil || d.Virtual || d.ID != 9 {
		t.Fatalf("destination %+v %v", d, err)
	}
	if _, err := h.authorizeHuman(t.Context(), human); err == nil {
		t.Fatal("replayed consumed capability")
	}
	if h.issueHuman(req) == nil {
		t.Fatal("reissued consumed capability")
	}
	human.Grant = a.TaskID
	if _, err := h.authorizeHuman(t.Context(), human); err == nil {
		t.Fatal("task token substituted")
	}
}
func TestAppMetadataFitsControlFrame(t *testing.T) {
	o := appcontrol.Observation{Elements: make([]appcontrol.Element, 128)}
	for i := range o.Elements {
		o.Elements[i] = appcontrol.Element{Handle: strings.Repeat("h", 128), Role: strings.Repeat("\x00", 512), Title: strings.Repeat("\x00", 4096), Value: strings.Repeat("字", 4096)}
	}
	boundObservation(&o)
	b, err := json.Marshal(Response{App: &AppResponse{Observation: &o}})
	if err != nil || len(b) > MaxMessageBytes || !o.Truncated || len(o.Elements) == 0 {
		t.Fatalf("bad bounds size=%d nodes=%d truncated=%v err=%v", len(b), len(o.Elements), o.Truncated, err)
	}
}
func TestAppUnknownOutcomePreventsNewLease(t *testing.T) {
	h, a, f := appFixture(t)
	h.grant(appReq("app_grant", a))
	h.mu.Lock()
	h.fenceLocked(a.Resource)
	h.mu.Unlock()
	f.uncertain = true
	if _, err := h.execute(t.Context(), appReq("app_quiesce", a)); err == nil {
		t.Fatal("uncertainty ignored")
	}
	a.LeaseEpoch++
	if err := h.grant(appReq("app_grant", a)); err == nil {
		t.Fatal("unknown action auto-thawed")
	}
	if !errors.As(appRefusal("action_uncertain"), new(*appcontrol.Error)) {
		t.Fatal("missing typed error")
	}
}
