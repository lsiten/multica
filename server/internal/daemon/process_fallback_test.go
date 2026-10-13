package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

// TestDaemonRunFallsBackToInProcessWhenSeparateServiceStartFails is the
// regression for the fail-closed startup: when a configured separate process
// cannot be started (here the native executable is absent, so the ownership /
// reconcile gate can never be satisfied), Run must NOT exit. It has to clear
// the optimistic process flags and keep serving in-process, so tasks claimed
// later still run. This is the "runtime prior owner requires reconciliation"
// failure that used to abort the whole daemon.
func TestDaemonRunFallsBackToInProcessWhenSeparateServiceStartFails(t *testing.T) {
	t.Setenv(cli.TaskConfigRootEnv, t.TempDir())
	if err := cli.SaveCLIConfig(cli.CLIConfig{Token: "mul_owned_fixture"}); err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tokens/current/renew":
			_, _ = io.WriteString(w, `{"renewed":false,"expires_at":"2099-01-01T00:00:00Z"}`)
		case "/api/daemon/workspaces":
			_, _ = io.WriteString(w, "[]")
		case "/api/me":
			_, _ = io.WriteString(w, `{"id":"11111111-1111-4111-8111-8111-8111-8111"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	root := t.TempDir()
	d := New(Config{
		ServerBaseURL:   backend.URL,
		WorkspacesRoot:  filepath.Join(root, "workspaces"),
		DaemonID:        "fallback-fixture",
		HealthPort:      port,
		PollInterval:    time.Second,
		LaunchedBy:      "desktop",
		ProcessServices: []string{"environment", "application", "gateway"},
		// The native executable is absent, so every separate-process start
		// fails and the daemon must fall back to the in-process owners.
		NativeHostExecutable:         filepath.Join(root, "missing-native"),
		NativeVscreenPreferencesPath: filepath.Join(root, "native.json"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Error("daemon exited during shutdown: ", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("daemon shutdown did not complete")
		}
	})
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	health := func() HealthResponse {
		response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
		if err != nil {
			return HealthResponse{}
		}
		defer response.Body.Close()
		var result HealthResponse
		_ = json.NewDecoder(response.Body).Decode(&result)
		return result
	}
	waitApplicationManager(t, func() bool { return health().Status == "running" })

	// Health flips to "running" for local control readiness before the
	// service-start blocks complete, so poll until the blocks have cleared the
	// optimistic process flags (or the daemon exits, which would be the bug).
	deadline := time.Now().Add(20 * time.Second)
	for {
		select {
		case <-done:
			t.Fatal("daemon exited during startup despite a separate-service start failure")
		case <-time.After(100 * time.Millisecond):
		}
		if !d.environmentProcessMode() && !d.applicationProcessMode() && !d.gatewayProcessMode() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("service-start fallback did not clear process flags: env=%v app=%v gateway=%v",
				d.environmentProcessMode(), d.applicationProcessMode(), d.gatewayProcessMode())
		}
	}
}
