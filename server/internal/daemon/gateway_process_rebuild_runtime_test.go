//go:build darwin || linux

package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// startGatewayChildOnce builds a bootstrap, launches a real gateway child, and
// returns the process + a call helper + an explicit close (no t.Cleanup), so a
// single domain can be closed and re-acquired by a second child in one test.
func startGatewayChildOnce(t *testing.T, root string) (*runtimeproc.Process, func(string, any) (runtimeproc.Response, error), func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{
		Backend: "http://127.0.0.1:1", Account: "owned-account", DaemonID: "owned-daemon",
		Service: "gateway", Profile: "owned-fixture",
	}, "fixture/commit")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, _ = io.Copy(hash, file)
	file.Close()
	process, err := runtimeproc.Start(ctx, runtimeproc.LaunchConfig{
		Executable:     executable,
		SHA256:         hex.EncodeToString(hash.Sum(nil)),
		Environment:    runtimeChildEnv(),
		Bootstrap:      bootstrap,
		StartupTimeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(operation string, input any) (runtimeproc.Response, error) {
		status, err := process.Client.Health(ctx)
		if err != nil {
			return runtimeproc.Response{}, err
		}
		request, err := process.Client.Request(operation, status.Fence, marshalRaw(input))
		if err != nil {
			return runtimeproc.Response{}, err
		}
		return process.Client.Call(ctx, request)
	}
	closeChild := func() { _ = process.Close(); cancel() }
	return process, call, closeChild
}

// gatewayChildPID reads the child's own PID and ready state over the private
// runtime channel so a test can prove two children are distinct physical
// processes (not the same process reusing the domain).
func gatewayChildPID(t *testing.T, call func(string, any) (runtimeproc.Response, error)) int {
	t.Helper()
	resp, err := call("gateway.status", map[string]any{})
	if err != nil || resp.Receipt == nil || resp.Receipt.Error != nil {
		t.Fatalf("gateway.status failed: %+v %v", resp, err)
	}
	var live struct {
		PID   int    `json:"pid"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(resp.Receipt.Result, &live); err != nil || live.PID == 0 || live.PID == os.Getpid() || live.State != "ready" {
		t.Fatalf("gateway is not a live ready physical child: %s", resp.Receipt.Result)
	}
	return live.PID
}

// TestGatewayProcessRuntimeRebuildReAcquiresDomain is the F2 runtime acceptance
// for "rebuild": after the first gateway child stops and exits, the private
// domain is re-acquirable, a fresh distinct child can own the listener again,
// and re-registered routes serve traffic. The first child stops gracefully so
// its durable record becomes a "stopped" owner all of whose operations are
// completed; the rebuilt child then replaces it (no reconcile fabricated). The
// first child's Close waits for exit before the second acquires the domain.
func TestGatewayProcessRuntimeRebuildReAcquiresDomain(t *testing.T) {
	var mu sync.Mutex
	var hits int
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusTeapot)
	}))
	defer callback.Close()

	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "gateway-service")

	// First child owns the domain, serves one request, then exits.
	_, firstCall, firstClose := startGatewayChildOnce(t, root)
	firstPID := gatewayChildPID(t, firstCall)
	reg, err := firstCall("gateway.register", map[string]any{"path": "/mcp", "callback_url": callback.URL})
	if err != nil || reg.Receipt == nil || reg.Receipt.Error != nil {
		t.Fatalf("first register failed: %+v %v", reg, err)
	}
	var reg1 struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(reg.Receipt.Result, &reg1); err != nil || reg1.Endpoint == "" {
		t.Fatalf("first register returned no endpoint: %s", reg.Receipt.Result)
	}
	resp, err := http.Get(reg1.Endpoint)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("first forward failed: status=%d", resp.StatusCode)
	}
	// Gracefully stop the first child so its durable record becomes a "stopped"
	// owner with all operations completed; a rebuilt child may then replace it.
	// "stop" is a built-in (un-namespaced) control operation, not a gateway domain op.
	stopResp, err := firstCall("stop", map[string]any{})
	if err != nil || stopResp.Receipt == nil || stopResp.Receipt.Error != nil {
		t.Fatalf("first gateway stop failed: %+v %v", stopResp, err)
	}
	firstClose()
	t.Logf("OBSERVE first gateway child PID=%d served a request, stopped, and exited (parent PID=%d)", firstPID, os.Getpid())

	// The domain must be re-acquirable: a second, distinct child owns it and serves again.
	_, secondCall, secondClose := startGatewayChildOnce(t, root)
	t.Cleanup(secondClose)
	secondPID := gatewayChildPID(t, secondCall)
	if secondPID == firstPID || secondPID == os.Getpid() {
		t.Fatalf("rebuild did not hand the domain to a distinct physical child: first=%d second=%d parent=%d", firstPID, secondPID, os.Getpid())
	}
	reg2, err := secondCall("gateway.register", map[string]any{"path": "/mcp", "callback_url": callback.URL})
	if err != nil || reg2.Receipt == nil || reg2.Receipt.Error != nil {
		t.Fatalf("rebuild register failed: %+v %v", reg2, err)
	}
	var reg2r struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(reg2.Receipt.Result, &reg2r); err != nil || reg2r.Endpoint == "" {
		t.Fatalf("rebuild register returned no endpoint: %s", reg2.Receipt.Result)
	}
	resp2, err := http.Get(reg2r.Endpoint)
	if err != nil {
		t.Fatalf("rebuild request failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusTeapot {
		t.Fatalf("rebuild forward failed: status=%d", resp2.StatusCode)
	}
	mu.Lock()
	total := hits
	mu.Unlock()
	if total != 2 {
		t.Fatalf("callback served %d times across both children, want 2", total)
	}
	t.Logf("OBSERVE domain re-acquired; distinct child PID=%d owned the listener and served (callback hits=%d)", secondPID, total)
}
