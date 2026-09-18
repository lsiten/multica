package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4/pkg/media/h264reader"
)

// This provider replays the existing encoded test fixture, never opens capture.
type performanceBrowserProvider struct {
	opens, closes atomic.Int32
}

func (p *performanceBrowserProvider) Open(_ context.Context, source mirror.EncodedSource) (mirror.EncodedStream, error) {
	name := "video-1600x900.h264"
	if source.Width == 1280 {
		name = "video-1280x720.h264"
	}
	raw, err := os.ReadFile(filepath.Join("../mirror/testdata", name))
	if err != nil {
		return nil, err
	}
	reader, err := h264reader.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var sample []byte
	for {
		nal, err := reader.NextNAL()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		kind := nal.Data[0] & 31
		if kind == 7 || kind == 8 || kind == 5 {
			sample = append(sample, 0, 0, 0, 1)
			sample = append(sample, nal.Data...)
		}
		if kind == 5 {
			break
		}
	}
	p.opens.Add(1)
	return &performanceBrowserStream{owner: p, sample: sample, ticker: time.NewTicker(time.Second / 30)}, nil
}

type performanceBrowserStream struct {
	owner  *performanceBrowserProvider
	sample []byte
	ticker *time.Ticker
	once   sync.Once
}

func (s *performanceBrowserStream) Next(ctx context.Context) (mirror.EncodedSample, error) {
	select {
	case <-ctx.Done():
		return mirror.EncodedSample{}, ctx.Err()
	case now := <-s.ticker.C:
		return mirror.EncodedSample{AnnexB: s.sample, KeyFrame: true, PTSNanos: now.UnixNano(), DurationNanos: int64(time.Second / 30)}, nil
	}
}
func (s *performanceBrowserStream) ForceKeyframe() error { return nil }
func (s *performanceBrowserStream) Close() error {
	s.once.Do(func() { s.ticker.Stop(); s.owner.closes.Add(1) })
	return nil
}

func TestPerformanceChromiumPrivateProducerContract(t *testing.T) {
	if os.Getenv("VSCREEN_RUN_HEADLESS_SMOKE") != "1" {
		t.Skip("requires explicit VSCREEN_RUN_HEADLESS_SMOKE=1; owned headless only")
	}
	node := os.Getenv("VSCREEN_INTEROP_NODE")
	evidence := os.Getenv("VSCREEN_INTEROP_EVIDENCE")
	if !filepath.IsAbs(node) || !filepath.IsAbs(evidence) {
		t.Fatal("explicit absolute VSCREEN_INTEROP_NODE and VSCREEN_INTEROP_EVIDENCE required")
	}
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	p, _ := performanceTestProducer(t)
	provider := &performanceBrowserProvider{}
	p.capture.provider = provider
	started := time.Now()
	p.now = func() uint64 { return uint64(time.Since(started).Nanoseconds() + 1) }
	for _, id := range []string{"one", "two"} {
		key := protocol.ResourceKey{BackendIdentity: p.backendID, WorkspaceID: p.workspaceID, RuntimeID: id, UID: 501}
		source := native.SourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: key, Source: protocol.MirrorSource{Kind: protocol.MirrorSourceVirtual, SourceID: id}, NativeEpoch: "native", Generation: "generation"}, DisplayID: 1, GeometryRevision: 1, LogicalWidth: 1600, LogicalHeight: 900}
		p.sources[id] = performanceSource{SourceID: id, RuntimeID: id, SourceTag: 1, NativeEpoch: "native", Generation: "generation", source: source, Marker: performanceMarker{Columns: 20, CellSize: 8, Reason: "static_fixture_has_no_marker"}}
		runtime := mirror.NewRuntimeMirror(nil, time.Second)
		runtime.SetCaptureHub(p.hub)
		p.runtimes[id] = runtime
	}
	server := httptest.NewServer(p)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	script, err := filepath.Abs("../../../apps/desktop/scripts/vscreen-performance-go-interop.verify.mjs")
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"baseURL": server.URL, "nonce": p.config.Nonce, "evidence": evidence})
	cmd := exec.CommandContext(ctx, node, script)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), "MULTICA_RUN_VSCREEN_GUI_SMOKE=0")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err = cmd.Run()
	if writeErr := os.WriteFile(filepath.Join(evidence, "node-result.log"), append(out.Bytes(), stderr.Bytes()...), 0600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if err != nil {
		t.Fatalf("owned Node contract failed: %v; %s", err, stderr.String())
	}
	var result struct {
		Passed  bool `json:"passed"`
		Cleanup bool `json:"cleanup_confirmed"`
	}
	if err = json.Unmarshal(out.Bytes(), &result); err != nil || !result.Passed || !result.Cleanup {
		t.Fatalf("invalid result: %s %v", out.String(), err)
	}
	p.mu.Lock()
	remaining := len(p.peers)
	p.mu.Unlock()
	if remaining != 0 || provider.opens.Load() != provider.closes.Load() || provider.opens.Load() != 2 {
		t.Fatalf("cleanup peers=%d opens=%d closes=%d", remaining, provider.opens.Load(), provider.closes.Load())
	}
	t.Logf("SYNTHETIC LOCAL ONLY: real Go private HTTP/Pion and Chromium media DTLS/ICE matched; peer cleanup=0 provider opens=%d closes=%d; no native host/capture or LAN acceptance", provider.opens.Load(), provider.closes.Load())
}
