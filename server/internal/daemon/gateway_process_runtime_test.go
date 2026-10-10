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

// runtimeChildEnv mirrors the production allowlist the daemon hands a child so
// the spawned real binary is exercised exactly as the control parent would
// launch it (only an explicit environment, no MULTICA_TOKEN, no inherited PAT).
func runtimeChildEnv() map[string]string {
	env := map[string]string{}
	for _, name := range []string{"PATH", "HOME", "USER", "USERPROFILE", "SYSTEMROOT", "WINDIR", "TMP", "TEMP", "TMPDIR", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(name); ok {
			env[name] = value
		}
	}
	if env["HOME"] == "" {
		env["HOME"] = os.TempDir()
	}
	return env
}

// startRuntimeChild builds a bootstrap for one service, pins the running test
// binary by canonical path + SHA256, and launches it exactly as the control
// parent would. It returns the process plus a helper that drives one operation
// over the private runtime channel.
func startRuntimeChild(t *testing.T, service string, root, serviceParent string) (*runtimeproc.Process, func(string, any) (runtimeproc.Response, error)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{
		Backend: "http://127.0.0.1:1", Account: "owned-account", DaemonID: "owned-daemon",
		Service: service, Profile: "owned-fixture",
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
	_, err = io.Copy(hash, file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
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
	t.Cleanup(func() {
		if err := process.Close(); err != nil {
			t.Errorf("%s child reaped: %v", service, err)
		}
	})
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
	return process, call
}

// TestGatewayProcessRuntimeOwnsListenerAndForwards is the real-child runtime
// acceptance for F2: the gateway process owns a live listener, registers a
// forward route, relays an agent request to the control-owned callback over the
// private runtime channel, and revokes it exactly. This is the native/runtime
// evidence the in-process gatewayProcessClient contracts cannot produce.
func TestGatewayProcessRuntimeOwnsListenerAndForwards(t *testing.T) {
	var got struct {
		mu     sync.Mutex
		served int
		remote string
		header string
	}
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.mu.Lock()
		got.served++
		got.remote = r.RemoteAddr
		got.header = r.Header.Get("X-Multica-Route")
		got.mu.Unlock()
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("from-callback"))
	}))
	defer callback.Close()

	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "gateway-service")
	process, call := startRuntimeChild(t, "gateway", root, parent)
	t.Cleanup(func() {})

	// A physical child, not an in-process fake: the child must be a distinct PID
	// and report a live state over the private runtime channel.
	status, err := process.Client.Read(t.Context(), "gateway.status", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var live struct {
		PID   int    `json:"pid"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(status, &live); err != nil || live.PID == 0 || live.PID == os.Getpid() || live.State != "ready" {
		t.Fatalf("gateway is not a live physical child: %s", status)
	}
	t.Logf("OBSERVE gateway child PID=%d state=%s (parent PID=%d)", live.PID, live.State, os.Getpid())

	// register a forward route; the returned endpoint is the gateway's own
	// listener, distinct from the control callback.
	register, err := call("gateway.register", map[string]any{"path": "/mcp", "callback_url": callback.URL})
	if err != nil || register.Receipt == nil || register.Receipt.Error != nil {
		t.Fatalf("register failed: %+v %v", register, err)
	}
	var reg struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(register.Receipt.Result, &reg); err != nil || reg.Endpoint == "" {
		t.Fatalf("register returned no endpoint: %s", register.Receipt.Result)
	}
	if reg.Endpoint == callback.URL {
		t.Fatalf("endpoint is the callback, not the gateway listener: %s", reg.Endpoint)
	}
	t.Logf("OBSERVE gateway endpoint=%s callback=%s", reg.Endpoint, callback.URL)

	// An agent request to the gateway listener must be relayed to the callback.
	resp, err := http.Get(reg.Endpoint)
	if err != nil {
		t.Fatalf("agent request to gateway failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot || string(body) != "from-callback" {
		t.Fatalf("forward did not reach the callback: status=%d body=%s", resp.StatusCode, body)
	}
	got.mu.Lock()
	deferred := got.served
	got.mu.Unlock()
	if deferred != 1 {
		t.Fatalf("callback served %d times, want 1", deferred)
	}
	t.Logf("OBSERVE agent request forwarded to callback; callback served once")

	// revoke removes exactly the current registration; a later request 404s.
	if _, err := call("gateway.revoke", map[string]any{"path": "/mcp"}); err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	again, err := http.Get(reg.Endpoint)
	if err != nil {
		t.Fatalf("post-revoke request failed: %v", err)
	}
	again.Body.Close()
	if again.StatusCode != http.StatusNotFound {
		t.Fatalf("revoked route still served status=%d", again.StatusCode)
	}
	t.Logf("OBSERVE revoked route 404s (status=%d)", again.StatusCode)
}

// TestGatewayProcessRuntimeRejectsNonLoopbackCallback is the runtime proof of the
// F2 forward security boundary: a callback that is not a plain loopback http
// origin is refused at register, so a route can never be relayed to another host.
func TestGatewayProcessRuntimeForwardsOnlyToLoopback(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "gateway-service")
	_, call := startRuntimeChild(t, "gateway", root, parent)

	// The route is parked at register; the forwarder must fail closed at request
	// time so a request is never relayed to a non-loopback host.
	register, err := call("gateway.register", map[string]any{"path": "/mcp", "callback_url": "http://evil.example/forward"})
	if err != nil || register.Receipt == nil || register.Receipt.Error != nil {
		t.Fatalf("register transport failed: %+v %v", register, err)
	}
	var reg struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(register.Receipt.Result, &reg); err != nil || reg.Endpoint == "" {
		t.Fatalf("register returned no endpoint: %s", register.Receipt.Result)
	}
	resp, err := http.Get(reg.Endpoint)
	if err != nil {
		t.Fatalf("request to gateway failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("non-loopback forward did not fail closed: status=%d", resp.StatusCode)
	}
	t.Logf("OBSERVE non-loopback callback fails closed at forward (status=%d)", resp.StatusCode)
}
