//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestGatewayProcessRuntimeListenerSurvivesControlDisconnect is the F2 runtime
// acceptance for "survival": the gateway child owns a persistent listener
// independent of the control's request lifecycle. After serving agent traffic
// the child stays alive (it does not exit), and the listener stays reachable,
// so a control reconnect/restart can re-attach without losing the route table.
func TestGatewayProcessRuntimeListenerSurvivesControlDisconnect(t *testing.T) {
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
	process, call := startRuntimeChild(t, "gateway", root, parent)

	// Register and drive an agent request through the gateway listener.
	reg, err := call("gateway.register", map[string]any{"path": "/mcp", "callback_url": callback.URL})
	if err != nil || reg.Receipt == nil || reg.Receipt.Error != nil {
		t.Fatalf("register failed: %+v %v", reg, err)
	}
	var regResult struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(reg.Receipt.Result, &regResult); err != nil || regResult.Endpoint == "" {
		t.Fatalf("register returned no endpoint: %s", reg.Receipt.Result)
	}
	resp, err := http.Get(regResult.Endpoint)
	if err != nil {
		t.Fatalf("agent request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("forward failed: status=%d", resp.StatusCode)
	}
	mu.Lock()
	firstHits := hits
	mu.Unlock()
	if firstHits != 1 {
		t.Fatalf("callback served %d times, want 1", firstHits)
	}
	t.Logf("OBSERVE gateway served one agent request to the callback")

	// The child must still be alive (it owns the listener): Wait returns the
	// deadline, not a clean exit, for a live child.
	aliveCtx, aliveCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer aliveCancel()
	if err := process.Wait(aliveCtx); err == nil {
		t.Fatalf("gateway child exited unexpectedly; listener did not survive")
	}
	// It should still be reachable: a second request to the same route still
	// forwards to the callback.
	resp2, err := http.Get(regResult.Endpoint)
	if err != nil {
		t.Fatalf("post-survival request failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusTeapot {
		t.Fatalf("post-survival forward failed: status=%d", resp2.StatusCode)
	}
	mu.Lock()
	secondHits := hits
	mu.Unlock()
	if secondHits != 2 {
		t.Fatalf("callback served %d times after survival, want 2", secondHits)
	}
	t.Logf("OBSERVE gateway child stayed alive and the listener still forwards (callback hits=%d)", secondHits)
}
