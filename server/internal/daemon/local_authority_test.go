package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/multica-ai/multica/server/internal/cli"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestVerifiedLocalAuthorityBootsWithoutRemotePreflight(t *testing.T) {
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(503) }))
	defer backend.Close()
	cfg := Config{ServerBaseURL: backend.URL, Profile: "local", DaemonID: "device", WorkspacesRoot: t.TempDir()}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := New(cfg, logger)
	first.client.SetToken("private-credential")
	first.accountID = "verified-account"
	first.workspaces["workspace"] = &workspaceState{workspaceID: "workspace", runtimeIDs: []string{"runtime"}}
	first.runtimeIndex["runtime"] = Runtime{ID: "runtime", Provider: "fixture"}
	if err := first.saveLocalAuthority(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(first.localAuthorityPath())
	if err != nil || strings.Contains(string(raw), "private-credential") {
		t.Fatal("identity snapshot exposed credential")
	}
	restarted := New(cfg, logger)
	restarted.client.SetToken("private-credential")
	if err := restarted.preflightAuth(t.Context()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 || restarted.accountID != "verified-account" || len(restarted.allRuntimeIDs()) != 1 {
		t.Fatalf("offline startup waited on remote or lost scope: requests=%d account=%s", requests.Load(), restarted.accountID)
	}
	if id, err := restarted.verifiedAccount(t.Context()); err != nil || id != "verified-account" {
		t.Fatalf("local service identity waited for remote: %s %v", id, err)
	}
}

func TestDaemonRunServesLocalDuringOutageAndRecoversAuthority(t *testing.T) {
	t.Setenv(cli.TaskConfigRootEnv, t.TempDir())
	if err := cli.SaveCLIConfig(cli.CLIConfig{Token: "mul_owned_fixture"}); err != nil {
		t.Fatal(err)
	}
	var offline atomic.Bool
	offline.Store(true)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "temporary outage", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tokens/current/renew":
			_, _ = io.WriteString(w, `{"renewed":false,"expires_at":"2099-01-01T00:00:00Z"}`)
		case "/api/daemon/workspaces":
			_, _ = io.WriteString(w, "[]")
		case "/api/me":
			_, _ = io.WriteString(w, `{"id":"11111111-1111-4111-8111-111111111111"}`)
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
	d := New(Config{ServerBaseURL: backend.URL, WorkspacesRoot: filepath.Join(root, "workspaces"), DaemonID: "cold-fixture", HealthPort: port, NativeVscreenPreferencesPath: filepath.Join(root, "native.json"), PollInterval: time.Second, LaunchedBy: "desktop"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("local daemon shutdown waited for unavailable backend")
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
	waitApplicationManager(t, func() bool { state := health(); return state.Status == "running" && state.RemoteBootstrapPending })
	select {
	case err := <-done:
		t.Fatalf("outage killed local daemon: %v", err)
	default:
	}
	if len(d.allRuntimeIDs()) != 0 {
		t.Fatal("unverified cold bootstrap admitted runtime authority")
	}
	offline.Store(false)
	waitApplicationManager(t, func() bool { state := health(); return state.Status == "running" && !state.RemoteBootstrapPending })
	d.mu.Lock()
	account := d.accountID
	d.mu.Unlock()
	if account != "11111111-1111-4111-8111-111111111111" {
		t.Fatal("remote recovery did not verify account")
	}
	if _, err := os.Stat(d.localAuthorityPath()); err != nil {
		t.Fatalf("recovered authority not durable: %v", err)
	}
}

func TestLocalAuthorityRejectsChangedCredentialAndBackend(t *testing.T) {
	cfg := Config{ServerBaseURL: "https://first.invalid", Profile: "local", DaemonID: "device", WorkspacesRoot: t.TempDir()}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := New(cfg, logger)
	first.client.SetToken("first-credential")
	first.accountID = "first-account"
	if err := first.saveLocalAuthority(); err != nil {
		t.Fatal(err)
	}
	changed := New(cfg, logger)
	changed.client.SetToken("second-credential")
	if changed.loadLocalAuthority() {
		t.Fatal("credential switch adopted previous account")
	}
	cfg.ServerBaseURL = "https://second.invalid"
	other := New(cfg, logger)
	other.client.SetToken("first-credential")
	if other.loadLocalAuthority() {
		t.Fatal("backend switch adopted previous account")
	}
}

func TestColdRemoteBootstrapRemainsLocalWhileBackendUnavailable(t *testing.T) {
	requested := make(chan struct{}, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requested <- struct{}{}:
		default:
		}
		http.Error(w, "unavailable", 503)
	}))
	defer backend.Close()
	d := New(Config{ServerBaseURL: backend.URL, WorkspacesRoot: t.TempDir(), Profile: "offline"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetToken("profile-credential")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.awaitRemoteBootstrap(ctx) }()
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("bootstrap retry not attempted")
	}
	if !d.ready.Load() || !d.remoteBootstrapPending.Load() || len(d.allRuntimeIDs()) != 0 || d.accountID != "" {
		t.Fatal("cold bootstrap exposed remote authority or blocked local control")
	}
	select {
	case err := <-done:
		t.Fatalf("temporary outage ended daemon bootstrap: %v", err)
	default:
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("bootstrap ignored local shutdown: %v", err)
	}
}

func TestRemoteBootstrapRetriesOnlyTransientFailures(t *testing.T) {
	for _, status := range []int{401, 403, 400, 404} {
		if retryableBootstrapError(&requestError{StatusCode: status}) {
			t.Fatalf("authority/config rejection %d retried", status)
		}
	}
	for _, status := range []int{429, 500, 503} {
		if !retryableBootstrapError(&requestError{StatusCode: status}) {
			t.Fatalf("temporary backend error %d killed local bootstrap", status)
		}
	}
}
