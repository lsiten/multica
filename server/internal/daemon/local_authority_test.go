package daemon

import (
	"context"
	"errors"
	"github.com/multica-ai/multica/server/internal/cli"
	"io"
	"log/slog"
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

func TestRequiredProcessOwnersRejectsIncompleteSet(t *testing.T) {
	if requiredProcessOwnersConfigured(nil) {
		t.Fatal("empty process-owner set must not be accepted")
	}
	if requiredProcessOwnersConfigured([]string{"ai", "mirror", "application", "environment"}) {
		t.Fatal("partial process-owner set must not be accepted")
	}
	if !requiredProcessOwnersConfigured([]string{"ai", "mirror", "application", "environment", "gateway"}) {
		t.Fatal("complete process-owner set must be accepted")
	}
}

func TestDaemonRunFailsClosedWhenRequiredOwnerCannotStart(t *testing.T) {
	t.Setenv(cli.TaskConfigRootEnv, t.TempDir())
	if err := cli.SaveCLIConfigForProfile(cli.CLIConfig{Token: "local-authority-token"}, "strict-offline"); err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "remote unavailable", http.StatusServiceUnavailable)
	}))
	defer backend.Close()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		ServerBaseURL:                backend.URL,
		Profile:                      "strict-offline",
		DaemonID:                     "strict-offline-device",
		WorkspacesRoot:               filepath.Join(root, "workspaces"),
		NativeVscreenPreferencesPath: filepath.Join(root, "native.json"),
		NativeHostExecutable:         filepath.Join(root, "missing-native"),
		ProcessServices:              []string{"ai", "mirror", "application", "environment", "gateway"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := New(cfg, logger)
	first.client.SetToken("local-authority-token")
	first.accountID = "verified-account"
	if err := first.saveLocalAuthority(); err != nil {
		t.Fatal(err)
	}
	d := New(cfg, logger)
	runErr := d.Run(t.Context())
	if runErr == nil || !strings.Contains(runErr.Error(), "start required environment service") {
		t.Fatalf("Run() error = %v, want required-owner startup failure", runErr)
	}
	if d.remoteBootstrapPending.Load() {
		t.Fatal("failed strict startup must not leave remote bootstrap pending")
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
