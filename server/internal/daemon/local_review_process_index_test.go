package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func verifyIndexReviewProcess(t *testing.T, fixture processReviewFixture) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.checkout, "selected.txt"), []byte("index process fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input := protocol.LocalReviewCommand{TaskID: fixture.taskID, Path: fixture.checkout, Action: "index"}
	var state localIndexView
	if err := json.Unmarshal(fixture.run(input)["page"], &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Status.Files) != 1 || state.Status.Files[0].Path != "selected.txt" {
		t.Fatal("index read mixed historical changes")
	}
	input.Action, input.CommandID, input.VersionID, input.IndexID = "stage", "http-stage", state.VersionID, state.Status.IndexID
	input.Target, input.Branch, input.Head, input.Paths = state.Status.Branch, state.Status.Branch, state.Status.Head, []string{"selected.txt"}
	fixture.run(input)
	if got := worktreeTestGit(t, fixture.checkout, "diff", "--cached", "--name-only"); got != "selected.txt" {
		t.Fatal("wrong file staged", got)
	}
	input.Action = "index"
	if err := json.Unmarshal(fixture.run(input)["page"], &state); err != nil {
		t.Fatal(err)
	}
	input.Action, input.CommandID, input.VersionID, input.IndexID = "commit", "http-commit", state.VersionID, state.Status.IndexID
	input.Head, input.Paths, input.Message = state.Status.Head, nil, "commit through authenticated relay"
	var result struct {
		Result localreview.IndexOperationResult `json:"result"`
	}
	if err := json.Unmarshal(fixture.run(input)["page"], &result); err != nil {
		t.Fatal(err)
	}
	if result.Result.Commit == nil {
		t.Fatal("commit result missing")
	}
	commit := result.Result.Commit.Commit
	if worktreeTestGit(t, fixture.checkout, "rev-parse", "HEAD") != commit || worktreeTestGit(t, fixture.checkout, "show", "HEAD:selected.txt") != "index process fixture" {
		t.Fatal("runtime Git disagrees with response")
	}
	if err := json.Unmarshal(fixture.run(input)["page"], &result); err != nil {
		t.Fatal(err)
	}
	if result.Result.Commit == nil || result.Result.Commit.Commit != commit || worktreeTestGit(t, fixture.checkout, "rev-parse", "HEAD") != commit {
		t.Fatal("commit replay created another commit")
	}
}
