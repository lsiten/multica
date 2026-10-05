package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestSharedWorktreeDeliveryRemainsDurableUntilServerAccepts(t *testing.T) {
	defer noSleepRetry(t)()
	var recovered atomic.Bool
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !recovered.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	d := &Daemon{client: NewClient(server.URL), logger: slog.Default(), terminalReports: &terminalReportStore{dir: t.TempDir()}}
	taskID := "11112222-3333-4444-5555-aaaa00000001"
	worktree, err := execenv.PrepareLocalWorktree(execenv.LocalWorktreeParams{LocalPath: createWorktreeTestRepo(t), EnvRoot: t.TempDir(), AgentName: "J", TaskID: taskID, WorkspaceID: "ws", AgentID: "agent", ConversationID: "issue", ConversationKey: "issue", RetainCheckout: true}, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := worktree.BeginSharedExecution(t.Context(), execenv.SharedWorktreeDelivery{TaskID: taskID, Namespace: d.sharedWorktreeReportNamespace()}); err != nil {
		t.Fatal(err)
	}
	writeLifecycleFile(t, filepath.Join(worktree.Path, "delivery.txt"), "finished work")
	outcome, err := worktree.Finalize(d.logger)
	if err != nil || outcome.Commit == "" {
		t.Fatalf("worktree did not finalize: %+v %v", outcome, err)
	}
	pending, delivered := d.replayPendingWorktreeDeliveries(context.Background())
	if pending != 1 || delivered != 0 {
		t.Fatalf("failed delivery was dropped: pending=%d delivered=%d", pending, delivered)
	}
	receipts, err := execenv.PendingSharedWorktreeDeliveries(t.Context(), d.sharedWorktreeReportNamespace())
	if err != nil || len(receipts) != 1 || receipts[0].Commit != outcome.Commit {
		t.Fatalf("retry lost immutable receipt: %+v %v", receipts, err)
	}
	recovered.Store(true)
	pending, delivered = d.replayPendingWorktreeDeliveries(context.Background())
	if pending != 0 || delivered != 1 {
		t.Fatalf("healthy server did not settle receipt: %d/%d", pending, delivered)
	}
	before := calls.Load()
	d.replayPendingWorktreeDeliveries(context.Background())
	if calls.Load() != before {
		t.Fatal("accepted delivery was replayed again")
	}
}

func TestSharedWorktreeNoWorkReceiptIsReportedAndRetired(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["no_work"] != true || body["branch_name"] != "" || body["worktree_commit"] != "" {
			t.Errorf("empty checkout reported a removed branch: %+v", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	d := &Daemon{client: NewClient(server.URL), logger: slog.Default(), terminalReports: &terminalReportStore{dir: t.TempDir()}}
	taskID := "11112222-3333-4444-5555-aaaa00000002"
	w, err := execenv.PrepareLocalWorktree(execenv.LocalWorktreeParams{LocalPath: createWorktreeTestRepo(t), EnvRoot: t.TempDir(), AgentName: "J", TaskID: taskID}, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.BeginSharedExecution(t.Context(), execenv.SharedWorktreeDelivery{TaskID: taskID, Namespace: d.sharedWorktreeReportNamespace()}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Finalize(d.logger); err != nil {
		t.Fatal(err)
	}
	pending, delivered := d.replayPendingWorktreeDeliveries(t.Context())
	if pending != 0 || delivered != 1 || calls.Load() != 1 {
		t.Fatalf("empty delivery was not accepted: %d/%d calls=%d", pending, delivered, calls.Load())
	}
	d.replayPendingWorktreeDeliveries(t.Context())
	if calls.Load() != 1 {
		t.Fatal("settled empty delivery repeated")
	}
}
