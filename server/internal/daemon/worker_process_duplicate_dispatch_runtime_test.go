//go:build darwin || linux

package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWorkerProcessRuntimeDuplicateDispatchIsRejected is the F3 runtime
// acceptance for "control unique claim / duplicate dispatch": launching a
// second per-execution worker for the same claim must fail because the first
// worker already owns the private record, so control can never run two workers
// (and therefore two providers) for one execution.
func TestWorkerProcessRuntimeDuplicateDispatchIsRejected(t *testing.T) {
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
		WorkerProcessEnabled:         true,
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

	first, err := d.ensureWorkerProcess(ctx, "exec-1")
	if err != nil {
		t.Fatalf("first dispatch failed: %v", err)
	}
	t.Cleanup(first.close)
	if _, err := first.bind(ctx, workerRunInput{
		Grant:               validWorkerGrant(),
		CallbackURL:         "http://127.0.0.1:1/x",
		WorkerIDMatchesBind: true,
		LaunchAuthorized:    true,
		Uncertain:           false,
	}); err != nil {
		t.Fatalf("first bind failed: %v", err)
	}
	t.Logf("OBSERVE first worker owns the claim")

	// A second dispatch for the same claim must be rejected: the first worker
	// already owns the private record, so a second worker cannot launch.
	if second, err := d.ensureWorkerProcess(ctx, "exec-1"); err == nil {
		t.Fatalf("duplicate dispatch was accepted; control could run two workers for one claim")
	} else {
		t.Logf("OBSERVE duplicate dispatch rejected: %v", err)
		_ = second
	}
}
