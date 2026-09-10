package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRemoteSelectedMergePublishesOnlySelectedFilesAndReplays(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	for _, name := range []string{"chosen.txt", "excluded.txt"} {
		if err := os.WriteFile(filepath.Join(path, name), []byte(name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	worktreeTestGit(t, path, "add", ".")
	worktreeTestGit(t, path, "commit", "-m", "source")
	command := protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws1", TaskID: "task1", Path: path, Target: "main", Action: "manifest", ActorID: "owner"}
	response := d.runRemoteReview(t.Context(), command)
	if response.Error != "" {
		t.Fatal(response.Error)
	}
	var manifest pagedReviewManifest
	if err := json.Unmarshal(response.Page, &manifest); err != nil {
		t.Fatal(err)
	}
	command.Action, command.CommandID, command.VersionID, command.SnapshotID = "merge_selected", "selected-once", manifest.VersionID, manifest.VersionID
	command.Paths, command.Message = []string{"chosen.txt"}, "apply chosen file"
	response = d.runRemoteReview(t.Context(), command)
	if response.Error != "" {
		t.Fatal(response.Error)
	}
	var result localreview.SelectedMergeResult
	if err := json.Unmarshal(response.Page, &result); err != nil {
		t.Fatal(err)
	}
	if result.Commit == "" || worktreeTestGit(t, repo, "rev-parse", "HEAD") != result.Commit {
		t.Fatal("target not updated")
	}
	if strings.Contains(worktreeTestGit(t, repo, "ls-tree", "--name-only", "HEAD"), "excluded.txt") {
		t.Fatal("unselected file was merged")
	}
	key := localreview.RecordKey(localreview.Snapshot{Path: manifest.Header.Repository, Target: "selected-merge:" + command.CommandID})
	receipt, err := localreview.LoadRecord(root, key)
	if err != nil {
		t.Fatal(err)
	}
	receipt.State, receipt.MergedCommit = "selected_merge_prepared", ""
	if err := localreview.SaveRecord(root, key, receipt); err != nil {
		t.Fatal(err)
	}
	response = d.runRemoteReview(t.Context(), command)
	if response.Error != "" {
		t.Fatal(response.Error)
	}
	if worktreeTestGit(t, repo, "rev-parse", "HEAD") != result.Commit {
		t.Fatal("replay created another commit")
	}
	command.ActorID = "other"
	if result := d.runRemoteReview(t.Context(), command); result.Error == "" {
		t.Fatal("another actor reused receipt")
	}
}
