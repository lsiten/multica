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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func spawnWorkerChild(t *testing.T, callbackURL string) func(string, any) (runtimeproc.Response, error) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "worker-service", uuid.NewString())
	// Reuse the shared real-child launcher but give the worker its own root so
	// each child owns a distinct domain lock (one execution, one worker).
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
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{
		Backend: "http://127.0.0.1:1", Account: "owned-account", DaemonID: "owned-daemon",
		Service: "worker", Profile: "owned-fixture",
	}, "fixture/commit")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
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
	t.Cleanup(cancel)
	t.Cleanup(func() {
		if err := process.Close(); err != nil {
			t.Errorf("worker child reaped: %v", err)
		}
	})
	_ = callbackURL
	return func(operation string, input any) (runtimeproc.Response, error) {
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
}

func workerBindStatus(t *testing.T, call func(string, any) (runtimeproc.Response, error), in map[string]any) workerStatus {
	t.Helper()
	resp, err := call("worker.bind", in)
	if err != nil {
		t.Fatalf("bind transport failed: %v", err)
	}
	if resp.Receipt == nil || resp.Receipt.Error != nil {
		t.Fatalf("bind returned an error receipt: code=%s msg=%s", resp.Receipt.Error.Code, resp.Receipt.Error.Message)
	}
	var status workerStatus
	if err := json.Unmarshal(resp.Receipt.Result, &status); err != nil {
		t.Fatalf("unmarshal worker status: %v (%s)", err, resp.Receipt.Result)
	}
	return status
}

// TestWorkerProcessRuntimeBecomesReadyPhysicalChild is the runtime proof that the
// per-execution worker role actually launches as a distinct process and becomes
// ready over the private runtime channel without any in-process fake.
func TestWorkerProcessRuntimeBecomesReadyPhysicalChild(t *testing.T) {
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer callback.Close()
	call := spawnWorkerChild(t, callback.URL)

	status, err := call("worker.status", map[string]any{})
	if err != nil {
		t.Fatalf("worker.status failed: %v", err)
	}
	var live struct {
		PID   int    `json:"pid"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(status.Receipt.Result, &live); err != nil || live.PID == 0 || live.PID == os.Getpid() {
		t.Fatalf("worker is not a live physical child: %s", status.Receipt.Result)
	}
	if live.State == "starting" {
		t.Fatalf("worker never became ready: %s", status.Receipt.Result)
	}
	t.Logf("OBSERVE worker child PID=%d state=%s (parent PID=%d)", live.PID, live.State, os.Getpid())
}

// TestWorkerProcessRuntimeAdmitsLaunchableBind proves the F3 worker admits a
// launch only when the exact server-returned identity, a valid scoped grant and
// a current launch authorization are all present — over the real runtime
// channel, in the real worker process.
func TestWorkerProcessRuntimeAdmitsLaunchableBind(t *testing.T) {
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer callback.Close()
	call := spawnWorkerChild(t, callback.URL)

	grant := validWorkerGrant()
	status := workerBindStatus(t, call, map[string]any{
		"grant":                  grant,
		"callback_url":           callback.URL,
		"worker_id_matches_bind": true,
		"launch_authorized":      true,
		"uncertain":              false,
	})
	if !status.Launchable {
		t.Fatalf("launchable bind refused: launchable=%v reason=%s", status.Launchable, status.ReasonCode)
	}
	if !status.HasClient {
		t.Fatalf("launchable bind did not construct the execution client")
	}
	if status.State != "ready" {
		t.Fatalf("worker not ready after bind: %s", status.State)
	}
	t.Logf("OBSERVE worker admitted launchable bind execution=%s state=%s", status.ExecutionID, status.State)
}

// TestWorkerProcessRuntimeFailsClosedOnRefusedBinds proves the worker never
// launches on a refused bind: an uncertain ACK, a worker-id mismatch, a missing
// launch authorization, or a missing/invalid grant each yields a distinct
// refusal reason and a not-launchable worker, exactly as DecideWorkerLaunch
// decides — in the real worker process, over the private channel.
func TestWorkerProcessRuntimeFailsClosedOnRefusedBinds(t *testing.T) {
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer callback.Close()

	cases := []struct {
		name       string
		grant      protocol.ExecutionGrantResponse
		match      bool
		authorized bool
		uncertain  bool
		reason     string
	}{
		{name: "uncertain-ack", grant: validWorkerGrant(), match: true, authorized: true, uncertain: true, reason: WorkerAuthReasonUncertain},
		{name: "worker-id-mismatch", grant: validWorkerGrant(), match: false, authorized: true, uncertain: false, reason: WorkerAuthReasonWorkerMismatch},
		{name: "missing-launch-auth", grant: validWorkerGrant(), match: true, authorized: false, uncertain: false, reason: WorkerAuthReasonNoLaunchAuth},
		{name: "invalid-grant", grant: func() protocol.ExecutionGrantResponse { g := validWorkerGrant(); g.Token = "bad_token"; return g }(), match: true, authorized: true, uncertain: false, reason: WorkerAuthReasonNoGrant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := spawnWorkerChild(t, callback.URL)
			status := workerBindStatus(t, call, map[string]any{
				"grant":                  tc.grant,
				"callback_url":           callback.URL,
				"worker_id_matches_bind": tc.match,
				"launch_authorized":      tc.authorized,
				"uncertain":              tc.uncertain,
			})
			if status.Launchable {
				t.Fatalf("%s: worker launched on a refused bind", tc.name)
			}
			if status.ReasonCode != tc.reason {
				t.Fatalf("%s: got reason %q, want %q", tc.name, status.ReasonCode, tc.reason)
			}
			t.Logf("OBSERVE %s refused: reason=%s launchable=%v", tc.name, status.ReasonCode, status.Launchable)
		})
	}
}

// TestWorkerProcessRuntimeReportsResultToControl is the runtime proof of F3
// "result persisted first": after a launchable bind the per-execution worker
// produces a terminal result and reports it through its own scoped execution
// client over a real HTTP POST to control; control (the callback) receives the
// exact result. This is the result-persistence boundary in the real worker
// process, not a fake transport.
func TestWorkerProcessRuntimeReportsResultToControl(t *testing.T) {
	var got struct {
		path string
		body []byte
		auth string
	}
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.path = r.URL.Path
		got.body = body
		got.auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer callback.Close()

	call := spawnWorkerChild(t, callback.URL)
	grant := validWorkerGrant()
	bindStatus := workerBindStatus(t, call, map[string]any{
		"grant":                  grant,
		"callback_url":           callback.URL,
		"worker_id_matches_bind": true,
		"launch_authorized":      true,
		"uncertain":              false,
	})
	if !bindStatus.Launchable {
		t.Fatalf("bind not launchable: reason=%s", bindStatus.ReasonCode)
	}
	resp, err := call("worker.report", map[string]any{
		"operation": "complete",
		"payload":   map[string]any{"summary": "worker-finished", "exit_code": 0},
	})
	if err != nil {
		t.Fatalf("report transport failed: %v", err)
	}
	if resp.Receipt == nil || resp.Receipt.Error != nil {
		t.Fatalf("report failed in worker: code=%s msg=%s", resp.Receipt.Error.Code, resp.Receipt.Error.Message)
	}
	if !strings.HasSuffix(got.path, "/complete") {
		t.Fatalf("callback did not receive the result at the execution path: %q", got.path)
	}
	if got.auth == "" {
		t.Fatalf("callback received no authorization; the scoped grant was not carried")
	}
	if !strings.Contains(string(got.body), "worker-finished") {
		t.Fatalf("callback body missing the reported result: %s", got.body)
	}
	t.Logf("OBSERVE worker reported result to control: path=%s auth-set=%v body=%s", got.path, got.auth != "", got.body)
}
