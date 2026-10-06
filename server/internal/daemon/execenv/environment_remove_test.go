package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInterruptedRemovalOfMultipleRepositoriesPreservesRecreatedCheckout(t *testing.T) {
	workspace := t.TempDir()
	claim, err := ClaimEnvRoot(RootDirParams{WorkspacesRoot: workspace, WorkspaceID: "workspace", TaskID: "task"})
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	root := claim.RootDir()
	owner, err := ReadEnvRootOwner(root)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	linked := []environmentRemovalRepository{}
	for _, name := range []string{"first", "second"} {
		repo := newTestRepo(t)
		checkout := filepath.Join(root, "workdir", name)
		gitRun(t, repo, "worktree", "add", "-b", "owned", checkout)
		common := gitRun(t, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
		common, err = filepath.EvalSymlinks(common)
		if err != nil {
			t.Fatal(err)
		}
		linked = append(linked, environmentRemovalRepository{Common: common, Path: filepath.Join(canonical, "workdir", name)})
	}
	relative, err := filepath.Rel(workspace, root)
	if err != nil {
		t.Fatal(err)
	}
	receipt := EnvironmentRemovalReceipt{Owner: *owner, RelativeRoot: filepath.ToSlash(relative), Repositories: linked}
	trash := filepath.Join(workspace, ".environment-trash")
	if err := os.Mkdir(trash, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trash, "cleanup-interrupted.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, filepath.Join(trash, "cleanup-interrupted")); err != nil {
		t.Fatal(err)
	}
	// First unregister completed before the daemon crashed. A new checkout now
	// occupies the same path; the second still has the old missing registration.
	gitRun(t, linked[0].Common, "worktree", "remove", "--force", linked[0].Path)
	gitRun(t, linked[0].Common, "worktree", "add", "-b", "replacement", linked[0].Path)
	keep := filepath.Join(linked[0].Path, "keep.txt")
	if err := os.WriteFile(keep, []byte("new execution"), 0600); err != nil {
		t.Fatal(err)
	}
	pending, err := PendingEnvironmentRemovals(workspace)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%v error=%v", pending, err)
	}
	if err := FinishEnvironmentRemoval(t.Context(), workspace, "cleanup-interrupted", pending["cleanup-interrupted"]); err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(keep); err != nil || string(contents) != "new execution" {
		t.Fatalf("replacement changed: %q %v", contents, err)
	}
	for index, repo := range linked {
		listed := gitRun(t, repo.Common, "worktree", "list", "--porcelain")
		if strings.Contains(listed, repo.Path) != (index == 0) {
			t.Fatalf("wrong registrations: %s", listed)
		}
	}
	if entries, err := os.ReadDir(trash); err != nil || len(entries) != 0 {
		t.Fatalf("deletion left garbage: %v %v", entries, err)
	}
}

func TestRemovalRejectsReceiptForAnotherOwner(t *testing.T) {
	workspace := t.TempDir()
	claim, err := ClaimEnvRoot(RootDirParams{WorkspacesRoot: workspace, WorkspaceID: "workspace", TaskID: "task"})
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	root := claim.RootDir()
	relative, err := filepath.Rel(workspace, root)
	if err != nil {
		t.Fatal(err)
	}
	trash := filepath.Join(workspace, ".environment-trash")
	if err := os.Mkdir(trash, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, filepath.Join(trash, "cleanup-wrong")); err != nil {
		t.Fatal(err)
	}
	if err := writeEnvRootOwner(filepath.Join(trash, "cleanup-wrong"), "other", "task"); err != nil {
		t.Fatal(err)
	}
	receipt := EnvironmentRemovalReceipt{Owner: EnvRootOwner{WorkspaceID: "workspace", TaskID: "task"}, RelativeRoot: filepath.ToSlash(relative)}
	if err := FinishEnvironmentRemoval(t.Context(), workspace, "cleanup-wrong", receipt); err == nil {
		t.Fatal("foreign tombstone deleted")
	}
	if _, err := os.Stat(filepath.Join(trash, "cleanup-wrong")); err != nil {
		t.Fatal(err)
	}
}
