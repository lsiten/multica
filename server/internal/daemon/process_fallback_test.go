package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestDaemonRunRejectsMissingRequiredServiceOwner(t *testing.T) {
	t.Setenv(cli.TaskConfigRootEnv, t.TempDir())
	if err := cli.SaveCLIConfig(cli.CLIConfig{Token: "mul_owned_fixture"}); err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tokens/current/renew":
			_, _ = io.WriteString(w, `{"renewed":false,"expires_at":"2099-01-01T00:00:00Z"}`)
		case "/api/me":
			_, _ = io.WriteString(w, `{"id":"11111111-1111-4111-8111-8111-8111-8111"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()

	d := New(Config{
		ServerBaseURL:                backend.URL,
		WorkspacesRoot:               filepath.Join(t.TempDir(), "workspaces"),
		DaemonID:                     "required-owner-fixture",
		HealthPort:                   0,
		ProcessServices:              []string{"environment", "application", "gateway"},
		NativeHostExecutable:         filepath.Join(t.TempDir(), "missing-native"),
		NativeVscreenPreferencesPath: filepath.Join(t.TempDir(), "native.json"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("daemon accepted a profile without all required process owners")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not fail closed when required child ownership was unavailable")
	}
}
