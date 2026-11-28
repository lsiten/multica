package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRemoteIndexReadsExistingStagedContent(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	if err := os.WriteFile(filepath.Join(path, "selected.txt"), []byte("staged from runtime\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command := protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws1", TaskID: "task1", Path: path, Action: "index", ActorID: "owner"}
	result := d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var state localIndexView
	if err := json.Unmarshal(result.Page, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Status.Files) != 1 || !state.Status.Files[0].Untracked {
		t.Fatal("working file missing")
	}
	worktreeTestGit(t, path, "add", "selected.txt")
	command.Action = "index"
	result = d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if err := json.Unmarshal(result.Page, &state); err != nil {
		t.Fatal(err)
	}
	if state.StagedVersionID == "" {
		t.Fatal("staged preview identity missing")
	}
	preview := command
	preview.Action, preview.VersionID, preview.FilePath, preview.Target = "file", state.StagedVersionID, "selected.txt", state.Status.Branch
	if err := os.WriteFile(filepath.Join(path, "selected.txt"), []byte("newer working edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	previewResult := d.runRemoteReview(t.Context(), preview)
	if previewResult.Error != "" {
		t.Fatal(previewResult.Error)
	}
	var patch pagedReviewFile
	if err := json.Unmarshal(previewResult.Page, &patch); err != nil {
		t.Fatal(err)
	}
	found := false
	if patch.Page == nil {
		t.Fatal("staged patch missing")
	}
	for _, line := range patch.Page.Lines {
		if line.Text == "+newer working edit" {
			t.Fatal("staged endpoint returned live worktree")
		}
		if line.Text == "+staged from runtime" {
			found = true
		}
	}
	if !found {
		t.Fatal("staged endpoint omitted indexed content")
	}
}
