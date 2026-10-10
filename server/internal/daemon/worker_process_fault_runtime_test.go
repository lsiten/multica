//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWorkerProcessRuntimeCrashIsNotRelaunchedOrFalselyCompleted is the F3
// runtime fault-injection acceptance: when the per-execution worker child
// dies mid-lifecycle, the control parent must detect it and treat the outcome
// as uncertain — it must never fabricate a result or relaunch a second worker
// for the same claim. A lost ACK or a dead worker is an uncertainty, not a
// success, so the task is not falsely marked complete.
func TestWorkerProcessRuntimeCrashIsNotRelaunchedOrFalselyCompleted(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" || r.Header.Get("Authorization") != "Bearer fixture-pat" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"11111111-1111-4111-8111-111111111111"}`)
	}))
	defer backend.Close()

	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer callback.Close()

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

	client, err := d.ensureWorkerProcess(ctx, "exec-1")
	if err != nil {
		t.Fatalf("ensureWorkerProcess failed: %v", err)
	}
	t.Cleanup(client.close)

	// Bind a launchable execution; the bind reports the worker's own PID.
	status, err := client.bind(ctx, workerRunInput{
		Grant:               validWorkerGrant(),
		CallbackURL:         callback.URL,
		WorkerIDMatchesBind: true,
		LaunchAuthorized:    true,
		Uncertain:           false,
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	if status == nil || !status.Launchable || status.PID == 0 {
		t.Fatalf("bind did not produce a live launchable worker: %+v", status)
	}
	t.Logf("OBSERVE bound worker PID=%d", status.PID)

	// Kill the worker child mid-lifecycle.
	if proc, perr := os.FindProcess(status.PID); perr == nil {
		_ = proc.Signal(os.Kill)
	}

	// The control must detect the death: the next operation fails (uncertain),
	// never a fabricated success. Poll until the control sees the dead child.
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, runErr := client.run(ctx)
		if runErr != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("control never detected the dead worker (run succeeded)")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// A retry on the same client must stay uncertain (no relaunch): the client
	// has latched its uncertain state, so a subsequent bind returns the
	// uncertainty rather than driving a new worker for the same claim.
	_, secondErr := client.bind(ctx, workerRunInput{
		Grant:               validWorkerGrant(),
		CallbackURL:         callback.URL,
		WorkerIDMatchesBind: true,
		LaunchAuthorized:    true,
		Uncertain:           false,
	})
	if secondErr == nil {
		t.Fatalf("control relaid a bind after an uncertain worker; no-relaunch violated")
	}
	if !errors.Is(secondErr, context.Canceled) {
		t.Logf("OBSERVE post-crash retry is uncertain (no relaunch): %v", secondErr)
	} else {
		t.Fatalf("post-crash retry returned %v; expected an uncertainty", secondErr)
	}
}
