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

func verifySelectedReviewProcess(t *testing.T, fixture processReviewFixture) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.checkout, "unselected.txt"), []byte("must stay on source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, fixture.checkout, "add", "unselected.txt")
	worktreeTestGit(t, fixture.checkout, "commit", "-m", "unselected source change")
	source := worktreeTestGit(t, fixture.checkout, "rev-parse", "HEAD")
	input := protocol.LocalReviewCommand{TaskID: fixture.taskID, Path: fixture.checkout, Target: "main", Action: "manifest"}
	var manifest pagedReviewManifest
	if err := json.Unmarshal(fixture.run(input)["page"], &manifest); err != nil {
		t.Fatal(err)
	}
	input.Action, input.CommandID, input.VersionID, input.SnapshotID = "merge_selected", "selected-http-once", manifest.VersionID, manifest.VersionID
	input.Paths, input.Message = []string{"app.txt"}, "apply only chosen file"
	var result localreview.SelectedMergeResult
	if err := json.Unmarshal(fixture.run(input)["page"], &result); err != nil {
		t.Fatal(err)
	}
	if result.Commit == "" || len(result.Conflicts) != 0 {
		t.Fatal("selected merge did not succeed")
	}
	commit := result.Commit
	if worktreeTestGit(t, fixture.repo, "rev-parse", "HEAD") != commit {
		t.Fatal("target did not advance")
	}
	if strings.Contains(worktreeTestGit(t, fixture.repo, "ls-tree", "--name-only", "HEAD"), "unselected.txt") {
		t.Fatal("unselected content reached target")
	}
	if worktreeTestGit(t, fixture.checkout, "rev-parse", "HEAD") != source {
		t.Fatal("source branch was changed")
	}
	if err := json.Unmarshal(fixture.run(input)["page"], &result); err != nil {
		t.Fatal(err)
	}
	if result.Commit != commit || worktreeTestGit(t, fixture.repo, "rev-parse", "HEAD") != commit {
		t.Fatal("replayed merge created another commit")
	}
}
