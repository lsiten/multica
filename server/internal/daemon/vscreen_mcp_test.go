package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type vscreenToolFixture struct {
	display   vscreen.Display
	revision  uint64
	value     string
	uncertain bool
	grants    int
	revoked   bool
}

func (f *vscreenToolFixture) Ensure(context.Context, vscreen.ResourceKey) (vscreen.Display, error) {
	return f.display, nil
}
func (f *vscreenToolFixture) Quiesce(context.Context, vscreen.ResourceKey) error { return nil }
func (f *vscreenToolFixture) Dispose(context.Context, vscreen.ResourceKey) error { return nil }
func (f *vscreenToolFixture) Act(_ context.Context, a vscreen.Action) (vscreen.ActionResult, error) {
	if f.uncertain {
		return vscreen.ActionResult{Epoch: f.display.Epoch, Outcome: protocol.VscreenActionUncertain}, nil
	}
	if a.Action.Type != nil {
		f.value = a.Action.Type.Text
	}
	return vscreen.ActionResult{Epoch: f.display.Epoch, Outcome: protocol.VscreenActionVerified}, nil
}
func (f *vscreenToolFixture) Grant(context.Context, appcontrol.Authority, time.Duration) error {
	f.grants++
	return nil
}
func (f *vscreenToolFixture) Renew(context.Context, appcontrol.Authority, time.Duration) error {
	return nil
}
func (f *vscreenToolFixture) Revoke(context.Context, appcontrol.Authority) error {
	f.revoked = true
	return nil
}
func (f *vscreenToolFixture) ResumeApps(context.Context, appcontrol.Authority) error { return nil }
func (f *vscreenToolFixture) LaunchApp(context.Context, appcontrol.Authority, appcontrol.LaunchRequest) (appcontrol.Window, error) {
	return appcontrol.Window{Handle: "window"}, nil
}
func (f *vscreenToolFixture) ObserveApp(context.Context, appcontrol.Authority, string, bool) (appcontrol.Observation, error) {
	f.revision++
	im := image.NewRGBA(image.Rect(0, 0, 2, 1))
	im.Set(0, 0, color.RGBA{R: 255, A: 255})
	if f.value != "" {
		im.Set(1, 0, color.RGBA{G: 255, A: 255})
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, im)
	return appcontrol.Observation{Display: appcontrol.Display{Epoch: f.display.Epoch}, Window: appcontrol.Window{Handle: "window", SnapshotRevision: f.revision}, PNG: buf.Bytes(), Width: 2, Height: 1, Elements: []appcontrol.Element{{Handle: "entry", Value: f.value}}}, nil
}
func newVscreenToolFixture(t *testing.T) (*vscreenToolFixture, *vscreen.Actor) {
	t.Helper()
	f := &vscreenToolFixture{display: vscreen.Display{Resource: protocol.ResourceKey{BackendIdentity: "https://fixture.invalid", WorkspaceID: "workspace", RuntimeID: "runtime", UID: 501}, Epoch: protocol.VscreenEpoch{NativeEpoch: "native", DisplayGeneration: "display", GeometryRevision: 1}, DisplayID: 1}}
	a, err := vscreen.NewManager(f, vscreen.SystemClock{}).For(f.display.Resource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	return f, a
}
func mcpFixtureURL(t *testing.T, cfg json.RawMessage) string {
	t.Helper()
	var document struct {
		MCPServers map[string]struct {
			URL string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(cfg, &document); err != nil {
		t.Fatal(err)
	}
	return document.MCPServers[vscreenMCPName].URL
}
func mcpFixtureCall(t *testing.T, url, name string, args any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	response, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	if err = json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing MCP result: %v", body)
	}
	return result
}
func mcpFixtureText(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	if result["isError"] == true {
		t.Fatalf("tool failure: %v", result)
	}
	content := result["content"].([]any)
	var value map[string]any
	if err := json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestVscreenMCPProviderWireLifecycle(t *testing.T) {
	f, a := newVscreenToolFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := newVscreenExecution(ctx, Task{ID: "task", WorkspaceID: "workspace", RuntimeID: "runtime"}, a, f, nil)
	defer e.Close()
	cfg, s, err := startVscreenMCP(ctx, e.invoke)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	url := mcpFixtureURL(t, cfg)
	lease := mcpFixtureText(t, mcpFixtureCall(t, url, "vscreen_acquire", map[string]any{"request_id": "request"}))
	tx := lease["transaction_id"]
	first := mcpFixtureCall(t, url, "vscreen_launch_app", map[string]any{"transaction_id": tx, "bundle_id": "fixture"})
	meta := mcpFixtureText(t, first)
	content := first["content"].([]any)
	imageBlock := content[1].(map[string]any)
	if imageBlock["type"] != "image" || imageBlock["mimeType"] != "image/png" {
		t.Fatal(imageBlock)
	}
	pixels, err := base64.StdEncoding.DecodeString(imageBlock["data"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = png.Decode(bytes.NewReader(pixels)); err != nil {
		t.Fatal(err)
	}
	action := map[string]any{"transaction_id": tx, "window_handle": "window", "snapshot_revision": meta["snapshot_revision"], "action_id": "input", "sequence": 1, "action": map[string]any{"kind": "type", "type": map[string]any{"element_handle": "entry", "text": "中文 é"}}}
	mcpFixtureText(t, mcpFixtureCall(t, url, "vscreen_type", action))
	second := mcpFixtureCall(t, url, "vscreen_observe", map[string]any{"transaction_id": tx, "window_handle": "window"})
	mcpFixtureText(t, second)
	if second["content"].([]any)[1].(map[string]any)["data"] == imageBlock["data"] {
		t.Fatal("observation pixels did not change")
	}
	mcpFixtureText(t, mcpFixtureCall(t, url, "vscreen_release", map[string]any{"transaction_id": tx}))
	if f.value != "中文 é" || !f.revoked || f.grants != 1 {
		t.Fatalf("native fixture state: %+v", f)
	}
	cancel()
	s.Close()
	if response, err := http.Post(url, "application/json", strings.NewReader("{}")); err == nil {
		response.Body.Close()
		t.Fatal("expired task URL still reachable")
	}
}
func TestVscreenMCPBoundaryRefusals(t *testing.T) {
	f, a := newVscreenToolFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := newVscreenExecution(ctx, Task{ID: "task"}, a, f, nil)
	defer e.Close()
	cfg, s, err := startVscreenMCP(ctx, e.invoke)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	url := mcpFixtureURL(t, cfg)
	for _, args := range []map[string]any{{"runtime_id": "forged"}, {"pid": 123}, {"display_id": 1}, {"task_id": "other"}} {
		if mcpFixtureCall(t, url, "vscreen_status", args)["isError"] != true {
			t.Fatal("forged scope accepted")
		}
	}
	for _, test := range []struct {
		url, body string
		status    int
	}{{url + "forged", "{}", 404}, {url, strings.Repeat(" ", vscreenMCPMaxRequest+1), 413}} {
		response, err := http.Post(test.url, "application/json", strings.NewReader(test.body))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatalf("got %d want %d", response.StatusCode, test.status)
		}
	}
	if _, err = mergeVscreenMCP(json.RawMessage(`{"mcpServers":{"multica-vscreen":{"url":"evil"}}}`), cfg); err == nil {
		t.Fatal("reserved collision accepted")
	}
	merged, err := mergeVscreenMCP(json.RawMessage(`{"mcpServers":{"existing":{"command":"fixture"}}}`), cfg)
	if err != nil || !bytes.Contains(merged, []byte("existing")) {
		t.Fatal("existing config lost", err)
	}
}
func TestVscreenMCPQueuedTaskCancellation(t *testing.T) {
	f, a := newVscreenToolFixture(t)
	held, err := a.Acquire(context.Background(), vscreen.Transaction{TaskID: "other", ID: "held"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := newVscreenExecution(ctx, Task{ID: "task"}, a, f, nil)
	poll, stop := context.WithTimeout(ctx, time.Millisecond)
	defer stop()
	content, err := e.acquire(poll, vscreenToolArgs{RequestID: "ticket"})
	if err != nil || len(content) == 0 {
		t.Fatal(err)
	}
	cancel()
	e.Close()
	if a.Status().Waiting != 0 {
		t.Fatal("cancelled FIFO waiter retained")
	}
	if err = a.Release(context.Background(), held); err != nil {
		t.Fatal(err)
	}
	if a.Status().Lease.TaskID != "" {
		t.Fatal("cancelled task obtained authority")
	}
}
func TestVscreenMCPUnknownActionStopsAfterRevocation(t *testing.T) {
	f, a := newVscreenToolFixture(t)
	f.uncertain = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := false
	e := newVscreenExecution(ctx, Task{ID: "task"}, a, f, func(error) {
		if !f.revoked || !a.Status().Frozen {
			t.Error("provider stopped before revocation")
		}
		stopped = true
	})
	defer e.Close()
	content, err := e.acquire(ctx, vscreenToolArgs{RequestID: "one"})
	if err != nil {
		t.Fatal(err)
	}
	_ = content
	e.mu.Lock()
	tx := e.lease.TransactionID
	e.mu.Unlock()
	call := func(name string, args any) { raw, _ := json.Marshal(args); _, _ = e.invoke(ctx, name, raw) }
	call("vscreen_observe", map[string]any{"transaction_id": tx, "window_handle": "window"})
	call("vscreen_click", map[string]any{"transaction_id": tx, "window_handle": "window", "snapshot_revision": 1, "action_id": "click", "sequence": 1, "action": map[string]any{"kind": "click", "click": map[string]any{"element_handle": "button"}}})
	if !stopped {
		t.Fatal("uncertain native action did not stop provider")
	}
}
