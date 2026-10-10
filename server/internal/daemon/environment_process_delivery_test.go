package daemon

import (
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// TestEnvironmentServiceReadsAndRetiresSharedDelivery exercises the new
// environment-service delivery capabilities (the F1 item 3 routing target): the
// child must read a settled receipt from the shared directory cache it owns and
// retire it through the exact-generation comparison, without the unexported file
// handle crossing the process boundary.
func TestEnvironmentServiceReadsAndRetiresSharedDelivery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	namespace := t.TempDir()
	taskID := "11112222-3333-4444-5555-aaaa00000010"
	d := &Daemon{logger: slog.Default(), terminalReports: &terminalReportStore{dir: namespace}}
	worktree, err := execenv.PrepareLocalWorktree(execenv.LocalWorktreeParams{LocalPath: createWorktreeTestRepo(t), EnvRoot: t.TempDir(), AgentName: "J", TaskID: taskID, WorkspaceID: "ws", AgentID: "agent", ConversationID: "issue", ConversationKey: "issue", RetainCheckout: true}, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := worktree.BeginSharedExecution(t.Context(), execenv.SharedWorktreeDelivery{TaskID: taskID, Namespace: namespace}); err != nil {
		t.Fatal(err)
	}
	writeLifecycleFile(t, filepath.Join(worktree.Path, "delivery.txt"), "finished work")
	outcome, err := worktree.Finalize(d.logger)
	if err != nil || outcome.Commit == "" {
		t.Fatalf("worktree did not finalize: %+v %v", outcome, err)
	}

	owner, err := newEnvironmentPhysicalOwner(t.TempDir(), t.TempDir(), d.logger)
	if err != nil {
		t.Fatal(err)
	}
	svc := &environmentProcessService{physical: owner, daemon: d}

	raw, rerr := svc.read(t.Context(), runtimeproc.Request{Operation: "worktree-delivery.receipts", Payload: marshalRaw(struct {
		Namespace string `json:"namespace"`
	}{namespace})})
	if rerr != nil {
		t.Fatalf("read receipts: %v", rerr)
	}
	var receipts []execenv.SharedWorktreeDelivery
	if err := json.Unmarshal(raw, &receipts); err != nil {
		t.Fatalf("decode receipts: %v", err)
	}
	if len(receipts) != 1 {
		t.Fatalf("expected one receipt, got %d: %+v", len(receipts), receipts)
	}
	if receipts[0].Commit != outcome.Commit || receipts[0].FilePath == "" {
		t.Fatalf("child read did not carry the settled commit and path: %+v", receipts[0])
	}

	if _, ackErr := svc.mutate(t.Context(), runtimeproc.Request{Operation: "worktree-delivery.ack", Payload: marshalRaw(receipts[0])}); ackErr != nil {
		t.Fatalf("ack receipt: %v", ackErr)
	}
	pending, err := execenv.PendingSharedWorktreeDeliveries(t.Context(), namespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("child did not retire the accepted receipt: %+v", pending)
	}
}
