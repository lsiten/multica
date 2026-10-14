package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestRemoteReviewRelaysEditAndMergeEdit exercises the relayed edit path: the
// server relays the command to the owning runtime, which applies a compare-and
// swap write ("edit") or a three-way merge ("merge_edit"). A stale base is a
// 409; an unresolvable conflict keeps the runtime version (Conflict == true).
func TestRemoteReviewRelaysEditAndMergeEdit(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	worktreeTestGit(t, repo, "config", "user.name", "Review")
	worktreeTestGit(t, repo, "config", "user.email", "review@example.test")
	path := filepath.Join(root, "worktree")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)

	base := []byte("line one\n")
	if err := os.WriteFile(filepath.Join(path, "file.txt"), base, 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, path, "add", "file.txt")
	worktreeTestGit(t, path, "commit", "-m", "base")

	digest := func(content []byte) string {
		sum := sha256.Sum256(content)
		return hex.EncodeToString(sum[:])
	}
	fileContent := func() []byte {
		got, err := os.ReadFile(filepath.Join(path, "file.txt"))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	edit := func(command protocol.LocalReviewCommand) protocol.LocalReviewResult {
		command.RuntimeID, command.WorkspaceID, command.TaskID = "runtime", "ws1", "task1"
		command.Path, command.Target, command.RelativePath = path, "main", "file.txt"
		return d.runRemoteReview(context.Background(), command)
	}

	// 1. edit: a compare-and-swap write applies when the base still matches.
	res := edit(protocol.LocalReviewCommand{ID: "edit-1", Action: "edit", EditBaseSHA: digest(base), EditNew: "line one edited\n"})
	if res.Error != "" {
		t.Fatalf("edit failed: %s", res.Error)
	}
	var applied localreview.EditWorkingFileResult
	if err := json.Unmarshal(res.Edit, &applied); err != nil {
		t.Fatalf("edit result undecodable: %v", err)
	}
	if applied.NewSHA256 != digest([]byte("line one edited\n")) {
		t.Fatalf("unexpected new sha: %s", applied.NewSHA256)
	}
	if got := string(fileContent()); got != "line one edited\n" {
		t.Fatalf("working file not written: %q", got)
	}

	// 2. edit with a stale base is rejected and leaves the file untouched.
	res = edit(protocol.LocalReviewCommand{ID: "edit-2", Action: "edit", EditBaseSHA: digest(base), EditNew: "must not apply\n"})
	if res.Error == "" {
		t.Fatal("stale-base edit was accepted")
	}
	if got := string(fileContent()); got != "line one edited\n" {
		t.Fatalf("stale-base edit changed the file: %q", got)
	}

	// 3. merge_edit fast path: base equals the current content, so the edit is written as-is.
	current := []byte("line one edited\n")
	res = edit(protocol.LocalReviewCommand{ID: "merge-1", Action: "merge_edit", EditBase: string(current), EditNew: "line one merged\n"})
	if res.Error != "" {
		t.Fatalf("merge_edit fast path failed: %s", res.Error)
	}
	var merged localreview.MergeWorkingFileResult
	if err := json.Unmarshal(res.Edit, &merged); err != nil {
		t.Fatalf("merge result undecodable: %v", err)
	}
	if !merged.Merged || merged.Conflict {
		t.Fatalf("fast-path merge not clean: %+v", merged)
	}
	if got := string(fileContent()); got != "line one merged\n" {
		t.Fatalf("fast-path merge did not write: %q", got)
	}

	// 4. merge_edit conflict: the runtime version was changed concurrently, so an
	// unresolvable three-way merge keeps the runtime content and reports Conflict.
	runtimeVersion := []byte("B\n")
	if err := os.WriteFile(filepath.Join(path, "file.txt"), runtimeVersion, 0o600); err != nil {
		t.Fatal(err)
	}
	res = edit(protocol.LocalReviewCommand{ID: "merge-2", Action: "merge_edit", EditBase: "X\n", EditNew: "C\n"})
	if res.Error != "" {
		t.Fatalf("merge_edit returned an error instead of a conflict: %s", res.Error)
	}
	if err := json.Unmarshal(res.Edit, &merged); err != nil {
		t.Fatalf("conflict merge undecodable: %v", err)
	}
	if !merged.Conflict {
		t.Fatalf("conflict not reported: %+v", merged)
	}
	if got := string(fileContent()); got != "B\n" {
		t.Fatalf("runtime version was overwritten on conflict: %q", got)
	}
}
