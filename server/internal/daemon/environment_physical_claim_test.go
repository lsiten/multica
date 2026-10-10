package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestEnvironmentGCKeepsPendingPhysicalHandoff(t *testing.T) {
	root := t.TempDir()
	env, err := execenv.PreparePhysical(execenv.PhysicalPrepareParams{WorkspacesRoot: root, WorkspaceID: "workspace", TaskID: "task"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer env.ReleaseLock()
	reservation, identity, err := env.ReservePhysicalRoot("workspace", "task")
	if err != nil {
		t.Fatal(err)
	}
	env.ReleaseLock()
	d := &Daemon{cfg: Config{WorkspacesRoot: root}}
	if release, err := d.lockGCTaskDirectory(env.RootDir); err == nil {
		release()
		t.Fatal("GC acquired root between physical preparation and worker claim")
	}
	claim, err := execenv.ClaimPhysicalRoot(root, reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	if err = execenv.ConfirmPhysicalRoot(reservation, identity); err != nil {
		t.Fatal(err)
	}
	if release, err := d.lockGCTaskDirectory(env.RootDir); err == nil {
		release()
		t.Fatal("GC acquired worker-held kernel claim")
	}
	claim.Release()
	release, err := d.lockGCTaskDirectory(env.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestPersistentStoreClaimFailureAndCancellationPreventUse(t *testing.T) {
	d := newCodexStoreGuardDaemon()
	if err := d.markActiveStore(context.Background(), "relative-private-store"); err == nil {
		t.Fatal("failed kernel protection allowed private preparation")
	}
	if len(d.activeStores) != 0 {
		t.Fatal("failed store acquisition marked active")
	}
	store := filepath.Join(t.TempDir(), "owned-session")
	release, ok := d.reserveStoreForDeletion(store)
	if !ok {
		t.Fatal("owned deletion reservation unavailable")
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := d.markActiveStore(ctx, store); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled store admission did not stop: %v", err)
	}
	if len(d.activeStores) != 0 {
		t.Fatal("cancelled store admission became active")
	}
}
