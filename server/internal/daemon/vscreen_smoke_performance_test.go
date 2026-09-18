package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/internal/vscreen/native/capture"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type performanceNativeFake struct {
	mu              sync.Mutex
	next            uint32
	displays        map[uint32]protocol.ResourceKey
	fail            string
	closes          int
	missingCounters bool
	streams         *performanceProviderFake
}

func (f *performanceNativeFake) Call(_ context.Context, r native.Request) (native.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail == r.Operation {
		return native.Response{}, errors.New("injected")
	}
	out := native.Response{Epoch: protocol.VscreenEpoch{NativeEpoch: "native", DisplayGeneration: "generation", GeometryRevision: 1}}
	switch r.Operation {
	case "ensure":
		f.next++
		f.displays[f.next] = r.Resource
		out.Display = &native.Display{ID: f.next, Managed: true, LogicalWidth: 1600, LogicalHeight: 900}
	case "dispose":
		for id, key := range f.displays {
			if key == r.Resource {
				delete(f.displays, id)
			}
		}
	case "list":
		for id := range f.displays {
			out.Displays = append(out.Displays, native.Display{ID: id, Managed: true})
		}
		if !f.missingCounters {
			out.LiveResources = &capture.LiveResourceStats{Available: true}
			if f.streams != nil {
				f.streams.mu.Lock()
				out.LiveResources.CaptureSessions = uint32(f.streams.active)
				out.LiveResources.EncoderSessions = uint32(f.streams.active)
				f.streams.mu.Unlock()
			}
		}
	}
	return out, nil
}
func (f *performanceNativeFake) Sources(_ context.Context, key protocol.ResourceKey) ([]native.SourceDescriptor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, k := range f.displays {
		if k == key {
			return []native.SourceDescriptor{{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: key, Source: protocol.MirrorSource{Kind: protocol.MirrorSourceVirtual, SourceID: "owned-source"}, NativeEpoch: "native", Generation: "generation"}, DisplayID: id, GeometryRevision: 1, LogicalWidth: 1600, LogicalHeight: 900}}, nil
		}
	}
	return nil, errors.New("source_missing")
}
func (*performanceNativeFake) Grant(context.Context, appcontrol.Authority, time.Duration) error {
	return nil
}
func (*performanceNativeFake) ResumeApps(context.Context, appcontrol.Authority) error { return nil }
func (*performanceNativeFake) LaunchApp(context.Context, appcontrol.Authority, appcontrol.LaunchRequest) (appcontrol.Window, error) {
	return appcontrol.Window{}, errors.New("test_must_not_launch")
}
func (*performanceNativeFake) Revoke(context.Context, appcontrol.Authority) error { return nil }
func (*performanceNativeFake) OwnedPID() int                                      { return 2 }
func (f *performanceNativeFake) Close() error {
	f.closes++
	if f.fail == "close" {
		return errors.New("close_unknown")
	}
	return nil
}

type performanceProviderFake struct {
	mu                    sync.Mutex
	opens, closes, active int
	emit                  bool
}
type performanceStreamFake struct {
	p    *performanceProviderFake
	once sync.Once
	sent bool
}

func (f *performanceProviderFake) Open(context.Context, mirror.EncodedSource) (mirror.EncodedStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens++
	f.active++
	return &performanceStreamFake{p: f}, nil
}
func (s *performanceStreamFake) Next(ctx context.Context) (mirror.EncodedSample, error) {
	if s.p.emit && !s.sent {
		s.sent = true
		return mirror.EncodedSample{AnnexB: []byte{0, 0, 0, 1, 0x65}, KeyFrame: true, DurationNanos: 33333333}, nil
	}
	<-ctx.Done()
	return mirror.EncodedSample{}, ctx.Err()
}
func (*performanceStreamFake) ForceKeyframe() error { return nil }
func (s *performanceStreamFake) Close() error {
	s.once.Do(func() { s.p.mu.Lock(); s.p.active--; s.p.closes++; s.p.mu.Unlock() })
	return nil
}

type performanceFixtureFake struct {
	fail  bool
	stops int
}

func (*performanceFixtureFake) BeforeLaunch() {}
func (*performanceFixtureFake) Read() (smokefixture.State, error) {
	return smokefixture.State{}, errors.New("synthetic")
}
func (f *performanceFixtureFake) Stop(context.Context) error {
	f.stops++
	if f.fail {
		return errors.New("fixture_unknown")
	}
	return nil
}

func performanceTestProducer(t *testing.T) (*performanceProducer, *performanceNativeFake) {
	t.Helper()
	provider := &performanceProviderFake{emit: true}
	client := &performanceNativeFake{displays: map[uint32]protocol.ResourceKey{}, streams: provider}
	p := &performanceProducer{config: VscreenPerformanceSmokeConfig{SchemaVersion: 1, Nonce: strings.Repeat("a", 64), ParentPID: os.Getppid(), EvidenceDir: t.TempDir(), Mode: "debug", DurationMS: 1000, Requested: VscreenPerformanceRequest{1600, 900, 30}}, started: time.Now(), now: func() uint64 { return 123456 }, client: client, capture: &performanceCapture{provider: provider}, runtimes: map[string]*mirror.RuntimeMirror{}, sources: map[string]performanceSource{}, peers: map[string]performancePeer{}, resources: map[string]performanceResources{"go": unavailablePerformanceResources(), "native": unavailablePerformanceResources()}, processStarts: map[string]uint64{"go": 1, "native": 2}, pid: 1, nativePID: 2, finished: make(chan struct{}), workspaceID: "workspace", backendID: "https://performance.invalid", clockEpoch: "epoch", readProcess: func(pid int) (performanceProcessReading, error) {
		return performanceProcessReading{Start: uint64(pid), RSS: 100, CPU: 200, FD: 4}, nil
	}, readGPU: func() (performanceGPUSample, error) { return performanceGPUSample{}, errPerformanceMetricUnavailable }}
	p.hub = mirror.NewCaptureHub(p.capture)
	file, err := os.CreateTemp(t.TempDir(), "journal")
	if err != nil {
		t.Fatal(err)
	}
	p.journal = file
	t.Cleanup(func() { p.finish(nil) })
	return p, client
}
func performanceTestRequest(p *performanceProducer, method, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.RemoteAddr = "127.0.0.1:3000"
	r.Header.Set("Authorization", "Bearer "+p.config.Nonce)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	return w
}

func TestPerformanceNoOptInAndConfigScope(t *testing.T) {
	t.Setenv("MULTICA_RUN_VSCREEN_GUI_SMOKE", "0")
	path := filepath.Join(t.TempDir(), "not-created", "config")
	if err := RunVscreenPerformanceSmoke(t.Context(), path, "fixture/commit", &bytes.Buffer{}); err == nil || err.Error() != "gui_not_authorized" {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("unauthorized side effect")
	}
	p, _ := performanceTestProducer(t)
	if validatePerformanceConfig(p.config, os.Getppid()) != nil {
		t.Fatal("valid debug rejected")
	}
	for _, change := range []func(*VscreenPerformanceSmokeConfig){func(c *VscreenPerformanceSmokeConfig) { c.ParentPID++ }, func(c *VscreenPerformanceSmokeConfig) { c.Nonce = "bad" }, func(c *VscreenPerformanceSmokeConfig) { c.Mode = "acceptance" }, func(c *VscreenPerformanceSmokeConfig) { c.Requested.Width = 1280 }} {
		c := p.config
		change(&c)
		if validatePerformanceConfig(c, os.Getppid()) == nil {
			t.Fatal("invalid scope/budget accepted")
		}
	}
}
func TestPerformanceHTTPAuthSourceAndClock(t *testing.T) {
	p, _ := performanceTestProducer(t)
	for _, tc := range []struct {
		remote, origin, auth string
		status               int
	}{{"127.0.0.1:1", "", "", 401}, {"192.0.2.1:1", "", "Bearer " + p.config.Nonce, 403}, {"127.0.0.1:1", "https://foreign.invalid", "Bearer " + p.config.Nonce, 403}} {
		r := httptest.NewRequest("GET", "/clock", nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Authorization", tc.auth)
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status=%d", w.Code)
		}
	}
	w := performanceTestRequest(p, "GET", "/clock", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"host_receive_ns":"123456"`) {
		t.Fatal(w.Body.String())
	}
	calls := 0
	p.now = func() uint64 { calls++; return uint64(200 - calls*100) }
	if w = performanceTestRequest(p, "GET", "/clock", nil); w.Code != 503 {
		t.Fatal("backwards clock accepted")
	}
	if w = performanceTestRequest(p, "POST", "/offer", performanceOfferRequest{ViewerID: "viewer", SourceID: "foreign", Offer: mirror.SessionDescription{Type: "offer", SDP: "synthetic"}}); w.Code != 403 {
		t.Fatal("foreign source accepted")
	}
}
func TestPerformanceMarkerProjectionRejectsPretend900p(t *testing.T) {
	source := native.SourceDescriptor{X: 2000, Y: 100, LogicalWidth: 1600, LogicalHeight: 900}
	marker := smokefixture.PerformanceMarker{X: 2016, Y: 116, CellSize: 8, Columns: 20}
	got := projectPerformanceMarker(marker, source, 1600, 900)
	if !got.Available || got.X != 16 || got.CellSize != 8 {
		t.Fatal(got)
	}
	got = projectPerformanceMarker(marker, source, 1280, 720)
	if got.Available || got.CellSize != 6.4 {
		t.Fatal("fractional marker pretended valid", got)
	}
}
func TestPerformanceCleanupUnknownNeverPasses(t *testing.T) {
	for _, failure := range []string{"quiesce", "dispose", "list", "close", "fixture", "counters"} {
		t.Run(failure, func(t *testing.T) {
			p, client := performanceTestProducer(t)
			key := protocol.ResourceKey{BackendIdentity: p.backendID, WorkspaceID: p.workspaceID, RuntimeID: "runtime", UID: uint32(os.Getuid())}
			client.displays[1] = key
			fixture := &performanceFixtureFake{fail: failure == "fixture"}
			p.owned = []*performanceOwned{{key: key, epoch: protocol.VscreenEpoch{NativeEpoch: "native", DisplayGeneration: "generation", GeometryRevision: 1}, displayID: 1, fixture: fixture}}
			client.fail = failure
			client.missingCounters = failure == "counters"
			result := p.finish(context.Canceled)
			if result.CleanupConfirmed || len(result.Errors) == 0 || client.closes != 1 || fixture.stops != 1 {
				t.Fatalf("result=%+v closes=%d stops=%d", result, client.closes, fixture.stops)
			}
			p.finish(nil)
			if client.closes != 1 {
				t.Fatal("finish repeated resource mutation")
			}
		})
	}
}
func TestPerformanceCyclesActuallyOpenCaptureAndProveCleanup(t *testing.T) {
	p, client := performanceTestProducer(t)
	p.config.Cycles = 3
	cycles, err := p.runCycles(t.Context(), 3)
	if err != nil || len(cycles) != 3 {
		t.Fatal(cycles, err)
	}
	for _, c := range cycles {
		if !c.CaptureOpened || !c.EncodedFrameReceived || !c.Disposed || !c.MeasurementsAvailable || *c.ActiveEncodersAfter != 0 || *c.ManagedDisplaysAfter != 0 {
			t.Fatalf("incomplete cycle %+v", c)
		}
	}
	client.streams.mu.Lock()
	defer client.streams.mu.Unlock()
	if client.streams.opens != 3 || client.streams.closes != 3 {
		t.Fatal("cycles did not exercise capture")
	}
}
func TestPerformanceResourceFailureIsUnavailableNotZero(t *testing.T) {
	p, client := performanceTestProducer(t)
	client.missingCounters = true
	p.readProcess = func(int) (performanceProcessReading, error) {
		return performanceProcessReading{}, errPerformanceMetricUnavailable
	}
	p.sampleResources(t.Context())
	nativeStats := p.resources["native"]
	if nativeStats.Availability["rss"].Available || nativeStats.Availability["callbacks"].Available || len(nativeStats.Samples) != 1 || nativeStats.Samples[0].RSS != nil || nativeStats.Samples[0].ActiveCallbacks != nil {
		t.Fatal("missing metrics invented zero")
	}
}

func TestPerformanceCancellationCanConfirmCleanupButNeverAcceptance(t *testing.T) {
	p, _ := performanceTestProducer(t)
	result := p.finish(context.Canceled)
	if !result.CleanupConfirmed {
		t.Fatal("confirmed cleanup missing")
	}
	found := false
	for _, reason := range result.Errors {
		if reason == "performance_run_cancelled_or_failed" {
			found = true
		}
	}
	if !found {
		t.Fatal("cancelled run lost failure")
	}
}

func TestPerformanceNativeIdleWaitHonorsCancellationWithoutZeroing(t *testing.T) {
	p, client := performanceTestProducer(t)
	client.streams.mu.Lock()
	client.streams.active = 1
	client.streams.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	response, err := p.awaitNativeIdle(ctx)
	if err == nil || response.LiveResources == nil || response.LiveResources.EncoderSessions != 1 {
		t.Fatal("unconfirmed counter converted to zero", response, err)
	}
	client.streams.mu.Lock()
	client.streams.active = 0
	client.streams.mu.Unlock()
}
