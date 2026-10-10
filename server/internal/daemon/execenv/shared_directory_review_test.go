package execenv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSharedReviewAdmissionProtectsAncestorAndChild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	root := t.TempDir()
	child := filepath.Join(root, "src")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	lease, err := UseSharedDirectory(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Finish(ctx, nil)
	for _, path := range []string{root, child, filepath.Join(child, "nested")} {
		if release, ok, err := ReserveSharedDirectoryPaths(ctx, []string{path}); err != nil || ok {
			if release != nil {
				release()
			}
			t.Fatalf("active overlap admitted %s: %v %v", path, ok, err)
		}
	}
	release, ok, err := ReserveSharedDirectoryPaths(ctx, []string{t.TempDir()})
	if err != nil || !ok {
		t.Fatalf("unrelated review refused: %v %v", ok, err)
	}
	release()
	if err = lease.Finish(ctx, nil); err != nil {
		t.Fatal(err)
	}
	release, ok, err = ReserveSharedDirectoryPaths(ctx, []string{root})
	if err != nil || !ok {
		t.Fatalf("released review unavailable: %v %v", ok, err)
	}
	deadline, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	if admitted, err := UseSharedDirectory(deadline, child); err == nil {
		admitted.Finish(ctx, nil)
		t.Fatal("new borrower entered during review")
	}
	cancel()
	release()
	after, err := UseSharedDirectory(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	if err = after.Finish(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPhysicalRootResetKeepsBorrowerAndPendingDelivery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	root := t.TempDir()
	env, err := PreparePhysical(PhysicalPrepareParams{WorkspacesRoot: root, WorkspaceID: "workspace", TaskID: "original"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer env.ReleaseLock()
	if err = os.WriteFile(filepath.Join(env.WorkDir, "keep.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := UseSharedDirectory(ctx, env.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Finish(ctx, nil)
	if err = lease.bindWorktreeRun(SharedWorktreeDelivery{TaskID: "borrower", Namespace: "owned-reset-test", WorkDir: env.WorkDir, Branch: "branch"}); err != nil {
		t.Fatal(err)
	}
	if err = resetEnvRootContents(env.RootDir); err == nil {
		t.Fatal("reset deleted live borrowed code")
	}
	if err = lease.Finish(ctx, func(bool) error { return resolveSharedWorktreeReceipts(lease.stateDir, "commit") }); err != nil {
		t.Fatal(err)
	}
	if err = resetEnvRootContents(env.RootDir); err == nil {
		t.Fatal("reset deleted undelivered code")
	}
	receipts, err := PendingSharedWorktreeDeliveries(ctx, "owned-reset-test")
	if err != nil || len(receipts) != 1 {
		t.Fatalf("pending receipt unavailable: %v %v", receipts, err)
	}
	if err = AcknowledgeSharedWorktreeDelivery(ctx, receipts[0]); err != nil {
		t.Fatal(err)
	}
	if err = resetEnvRootContents(env.RootDir); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(env.WorkDir, "keep.txt")); !os.IsNotExist(err) {
		t.Fatalf("safe reset did not complete: %v", err)
	}
}

func TestSourcePreparationReservationDoesNotDirtySnapshotInput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	repo := newTestRepo(t)
	before, err := runGitTrimmed(repo, "status", "--porcelain")
	if err != nil || before != "" {
		t.Fatalf("owned repository baseline dirty: %q %v", before, err)
	}
	early, err := UseSharedDirectory(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	dirty, err := runGitTrimmed(repo, "status", "--porcelain")
	if err != nil || !strings.Contains(dirty, ".multica/") {
		t.Fatalf("expected managed-marker regression evidence: %q %v", dirty, err)
	}
	t.Logf("OBSERVE premature ordinary admission writes managed marker: git status=%q", dirty)
	if err = early.Finish(ctx, nil); err != nil {
		t.Fatal(err)
	}
	reservation, err := ReserveSharedDirectoryPreparation(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Finish(ctx, nil)
	clean, err := runGitTrimmed(repo, "status", "--porcelain")
	if err != nil || clean != "" {
		t.Fatalf("preparation reservation dirtied snapshot input: %q %v", clean, err)
	}
	t.Log("OBSERVE preparation reservation holds actual FD with empty Git status; no user reset/clean")
	if release, ok, err := ReserveSharedDirectoryPaths(ctx, []string{repo}); err != nil || ok {
		if release != nil {
			release()
		}
		t.Fatalf("invisible reservation failed review exclusion: %v %v", ok, err)
	}
	worker, err := UseSharedDirectory(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if err = reservation.Finish(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err = worker.Finish(ctx, nil); err != nil {
		t.Fatal(err)
	}
	after, err := runGitTrimmed(repo, "status", "--porcelain")
	if err != nil || after != "" {
		t.Fatalf("managed marker restoration changed user checkout: %q %v", after, err)
	}
}
