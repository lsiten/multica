//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGatewayProcessHotpathOptInLaunchesRealChild is the runtime acceptance for
// the F2 "actual extraction": with process opt-in ON, the real daemon startup
// path (ensureGatewayProcess) must launch a distinct physical child that owns
// the listener, not fall back to the in-process broker. This is the daemon
// path the prior in-process-only tests could not exercise.
func TestGatewayProcessHotpathOptInLaunchesRealChild(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" || r.Header.Get("Authorization") != "Bearer fixture-pat" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"11111111-1111-4111-8111-111111111111"}`)
	}))
	defer backend.Close()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	d := New(Config{
		ProcessServices:              []string{"gateway"},
		ServerBaseURL:                backend.URL,
		DaemonID:                     "owned-daemon",
		Profile:                      "owned-fixture",
		WorkspacesRoot:               filepath.Join(root, "workspaces"),
		NativeHostExecutable:         executable,
		NativeHostBuild:              "fixture/commit",
		NativeVscreenPreferencesPath: filepath.Join(root, "vscreen-enabled.json"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetToken("fixture-pat")

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	gateway, err := d.ensureGatewayProcess(ctx)
	if err != nil {
		t.Fatalf("ensureGatewayProcess failed: %v", err)
	}
	t.Cleanup(func() {
		gateway.close()
	})
	if !gateway.ready() {
		t.Fatalf("gateway child is not ready")
	}

	// The child is a distinct physical process (not an in-process fake): its
	// status reports a PID different from the parent and a live state.
	status, err := gateway.process.Client.Read(t.Context(), "gateway.status", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("gateway.status read failed: %v", err)
	}
	var live struct {
		PID   int    `json:"pid"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(status, &live); err != nil || live.PID == 0 || live.PID == os.Getpid() || live.State != "ready" {
		t.Fatalf("gateway is not a live physical child: %s", status)
	}
	t.Logf("OBSERVE hot-path opt-in launched real gateway child PID=%d state=%s (parent PID=%d)", live.PID, live.State, os.Getpid())
}
