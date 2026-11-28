package daemon

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
)

func TestLocalReviewHistorySurvivesChangedSnapshot(t *testing.T) {
	// Given a decision saved on the owning runtime, without a backend database.
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	request := worktreeReviewRequest{TaskID: "task1", WorkspaceID: "ws1", Path: path, Target: "main"}
	wrongRuntime := request
	wrongRuntime.RuntimeID = "runtime-on-another-machine"
	if w := callReview(t, d, wrongRuntime, "test-token"); w.Code != http.StatusForbidden {
		t.Fatal("local review accepted another runtime", w.Code)
	}
	w := callReview(t, d, request, "test-token")
	var initial worktreeReviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if err := localreview.SaveRecord(root, localreview.RecordKey(initial.Snapshot), localreview.Record{SnapshotID: initial.ID, SourceHead: initial.Head, State: "approved", PreparedCommit: initial.Head, Snapshot: &initial.Snapshot, Events: []localreview.Event{{Kind: "approve", SnapshotID: initial.ID, Comment: "checked locally"}}}); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, path, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-m", "later change")

	// When the runtime reads a new snapshot from the same repository.
	request.Action = "read"
	w = callReview(t, d, request, "test-token")
	var response struct {
		Review struct {
			State  string `json:"state"`
			Events []struct {
				Kind       string `json:"kind"`
				SnapshotID string `json:"snapshot_id"`
				Comment    string `json:"comment"`
			} `json:"events"`
		} `json:"review"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Review map[string]json.RawMessage `json:"review"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"snapshot", "prepared_request", "prepared_commit"} {
		if _, present := wire.Review[field]; present {
			t.Fatal("private recovery data leaked", field)
		}
	}
	// Then approval is invalidated, but its history remains available locally.
	if response.Review.State != "draft" || len(response.Review.Events) != 1 {
		t.Fatalf("state=%s events=%+v", response.Review.State, response.Review.Events)
	}
	event := response.Review.Events[0]
	if event.Kind != "approve" || event.SnapshotID != initial.ID || event.Comment != "checked locally" {
		t.Fatalf("wrong local decision history: %+v", event)
	}
}
