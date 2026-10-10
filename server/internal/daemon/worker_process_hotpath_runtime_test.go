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

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestWorkerProcessHotpathOptInLaunchesRealChild is the runtime acceptance for
// the F3 "actual extraction": with worker opt-in ON, the real daemon path
// (ensureWorkerProcess) must launch a distinct physical per-execution worker
// child that the control parent then binds, rather than running the task
// in-process. The bind returns the worker's own PID, so the child is proven
// physical, not a fake transport.
func TestWorkerProcessHotpathOptInLaunchesRealChild(t *testing.T) {
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

	grant := validWorkerGrant()
	status, err := client.bind(ctx, workerRunInput{
		Grant:               grant,
		CallbackURL:         callback.URL,
		WorkerIDMatchesBind: true,
		LaunchAuthorized:    true,
		Uncertain:           false,
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	if status == nil || status.PID == 0 || status.PID == os.Getpid() {
		t.Fatalf("worker is not a live physical child: %+v", status)
	}
	if !status.Launchable {
		t.Fatalf("hot-path worker bind not launchable: reason=%s", status.ReasonCode)
	}
	t.Logf("OBSERVE hot-path opt-in launched real worker child PID=%d state=%s (parent PID=%d)", status.PID, status.State, os.Getpid())
	_ = protocol.ExecutionGrantResponse{}
}
