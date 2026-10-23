package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestEnvironmentReconcileRequiresDurableCurrentPreparation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	owner, err := newEnvironmentPhysicalOwner(t.TempDir(), t.TempDir(), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.close)
	input := execenv.PhysicalPrepareParams{WorkspaceID: "ws", TaskID: "task", AgentName: "fake"}
	result, err := owner.prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	service := &environmentProcessService{physical: owner}
	request := runtimeproc.Request{Operation: "physical.prepare", Payload: marshalRaw(input)}
	outcome := runtimeproc.Outcome{Result: marshalRaw(result)}
	if _, confirmed := service.reconcileMutation(t.Context(), request, outcome); !confirmed {
		t.Fatal("durable current preparation was not reconciled")
	}
	wrong := input
	wrong.TaskID = "different-task"
	if _, confirmed := service.reconcileMutation(t.Context(), runtimeproc.Request{Operation: request.Operation, Payload: marshalRaw(wrong)}, outcome); confirmed {
		t.Fatal("another task's preparation was accepted")
	}
	journal := filepath.Join(owner.journal, result.Reservation.ID+".json")
	raw, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	if _, confirmed := service.reconcileMutation(t.Context(), request, outcome); confirmed {
		t.Fatal("in-memory result without durable journal was accepted")
	}
	if err := os.WriteFile(journal, raw, 0600); err != nil {
		t.Fatal(err)
	}
	claim, err := execenv.ClaimPhysicalRoot(owner.root, result.Reservation)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.confirmRoot(result.Reservation); err != nil {
		claim.Release()
		t.Fatal(err)
	}
	claim.Release()
	if _, err := owner.prepare(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if _, confirmed := service.reconcileMutation(t.Context(), request, outcome); confirmed {
		t.Fatal("superseded root generation was accepted")
	}
}

func TestEnvironmentReconcileFinishRequiresExactDurablePermitAndOutcome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	owner, err := newEnvironmentPhysicalOwner(root, t.TempDir(), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.close)
	result, err := owner.prepare(t.Context(), execenv.PhysicalPrepareParams{WorkspaceID: "ws", TaskID: "task", AgentName: "fake", LocalWorktree: &execenv.LocalWorktreeParams{LocalPath: createWorktreeTestRepo(t), RetainCheckout: true}})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := execenv.ClaimPhysicalRoot(root, result.Reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	if err := owner.confirmRoot(result.Reservation); err != nil {
		t.Fatal(err)
	}
	worker := &execenv.LocalWorktree{Path: result.WorktreePath, WorkDir: result.WorkDir, Branch: result.Branch}
	if err := worker.BeginSharedExecution(t.Context(), execenv.SharedWorktreeDelivery{TaskID: "task", Namespace: owner.journal}); err != nil {
		t.Fatal(err)
	}
	participant, err := worker.PhysicalParticipant()
	if err != nil {
		t.Fatal(err)
	}
	permit, err := owner.beginFinish(t.Context(), result.Reservation.ID, participant)
	if err != nil {
		t.Fatal(err)
	}
	service := &environmentProcessService{physical: owner}
	begin := runtimeproc.Request{Operation: "physical.finish_begin", Payload: marshalRaw(map[string]any{"preparation_id": result.Reservation.ID, "participant": participant})}
	if _, confirmed := service.reconcileMutation(t.Context(), begin, runtimeproc.Outcome{Result: marshalRaw(permit)}); !confirmed {
		t.Fatal("exact persisted permit was not reconciled")
	}
	if err := worker.ReleasePhysicalParticipant(permit); err != nil {
		t.Fatal(err)
	}
	settled, err := owner.confirmFinish(result.Reservation.ID, permit)
	if err != nil {
		t.Fatal(err)
	}
	finish := runtimeproc.Request{Operation: "physical_finish_confirm", Payload: marshalRaw(map[string]any{"preparation_id": result.Reservation.ID, "permit": permit})}
	outcome := runtimeproc.Outcome{Result: marshalRaw(settled)}
	if _, confirmed := service.reconcileMutation(t.Context(), finish, outcome); !confirmed {
		t.Fatal("durable finish outcome was not reconciled")
	}
	permit.ID = "different-permit"
	finish.Payload = marshalRaw(map[string]any{"preparation_id": result.Reservation.ID, "permit": permit})
	if _, confirmed := service.reconcileMutation(t.Context(), finish, outcome); confirmed {
		t.Fatal("different permit was accepted for a completed finish")
	}
	if _, confirmed := service.reconcileMutation(t.Context(), runtimeproc.Request{Operation: "environment.operation", Payload: finish.Payload}, outcome); confirmed {
		t.Fatal("generic filesystem result was treated as proven completion")
	}
}
