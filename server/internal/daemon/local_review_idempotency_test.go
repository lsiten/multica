package daemon

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

func TestLocalReviewRepeatedApprovalPreservesLaterDecision(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	request := worktreeReviewRequest{WorkspaceID: "ws1", TaskID: "task1", Path: path, Target: "main", ActorID: "user1"}
	var snapshot worktreeReviewResponse
	if err := json.Unmarshal(callReview(t, d, request, "test-token").Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	request.SnapshotID, request.Action, request.CommandID = snapshot.ID, "approve", "approve-1"
	if w := callReview(t, d, request, "test-token"); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	later := request
	later.Action, later.CommandID = "request_changes", "changes-1"
	if w := callReview(t, d, later, "test-token"); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	// Replay the old operation after a newer decision, as with a delayed retry.
	w := callReview(t, d, request, "test-token")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Review.State != "changes_requested" || len(snapshot.Review.Events) != 2 {
		t.Fatalf("replayed approval rewrote current state: %+v", snapshot.Review)
	}
	request.Comment = "different payload with reused operation ID"
	if w := callReview(t, d, request, "test-token"); w.Code != http.StatusConflict {
		t.Fatal("accepted conflicting operation reuse", w.Code)
	}
}
