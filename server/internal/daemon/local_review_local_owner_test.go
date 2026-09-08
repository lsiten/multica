package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
)

func TestLocalReviewOwnerCanApproveWithoutCloudIdentity(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.workspaces = map[string]*workspaceState{"ws1": {runtimeIDs: []string{"runtime1"}}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	worktreeTestGit(t, repo, "config", "user.name", "Local Owner Test")
	worktreeTestGit(t, repo, "config", "user.email", "owner@example.test")
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	worktreeTestGit(t, path, "commit", "--allow-empty", "-m", "local change")
	if err := execenv.WriteReviewRuntime(root, execenv.ReviewRuntime{WorkspaceID: "ws1", TaskID: "task1", RuntimeID: "runtime1"}); err != nil {
		t.Fatal(err)
	}
	request := worktreeReviewRequest{WorkspaceID: "ws1", TaskID: "task1", RuntimeID: "runtime1", Path: path, Target: "main"}
	w := callReview(t, d, request, "test-token")
	var snapshot worktreeReviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	request.Action, request.SnapshotID, request.ActorID = "approve", snapshot.ID, "spoofed-user"
	missingID, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	missingRequest := httptest.NewRequest(http.MethodPost, "/worktrees/review", bytes.NewReader(missingID))
	missingRequest.Header.Set("Authorization", "Bearer test-token")
	missingRequest.Header.Set("X-Multica-Profile", d.cfg.Profile)
	w = httptest.NewRecorder()
	d.worktreeReviewHandler()(w, missingRequest)
	if w.Code != http.StatusBadRequest {
		t.Fatal("local decision accepted without operation ID", w.Code)
	}
	request.CommandID = "local-approve"
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/worktrees/review", bytes.NewReader(data))
	r.Header.Set("Authorization", "Bearer test-token")
	r.Header.Set("X-Multica-Profile", d.cfg.Profile)
	w = httptest.NewRecorder()
	d.worktreeReviewHandler()(w, r)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Review.State != "approved" || len(snapshot.Review.Events) != 1 || snapshot.Review.Events[0].ActorID != "local-runtime:runtime1" {
		t.Fatalf("local decision identity: %+v", snapshot.Review)
	}
	request.Action = "merge"
	request.CommandID = "local-merge"
	data, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	mergeRequest := httptest.NewRequest(http.MethodPost, "/worktrees/review", bytes.NewReader(data))
	mergeRequest.Header = r.Header.Clone()
	w = httptest.NewRecorder()
	d.worktreeReviewHandler()(w, mergeRequest)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Review.State != "merged" || snapshot.Review.MergedCommit != worktreeTestGit(t, repo, "rev-parse", "HEAD") {
		t.Fatal("local merge did not update target and receipt")
	}
	var wire struct {
		Review map[string]json.RawMessage `json:"review"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"snapshot", "prepared_request", "prepared_commit"} {
		if _, present := wire.Review[field]; present {
			t.Fatal("internal recovery data leaked into MR view", field)
		}
	}
	mergedCommit := snapshot.Review.MergedCommit
	// Reproduce a crash after the ref update but before the final receipt save.
	key := localreview.RecordKey(snapshot.Snapshot)
	prepared, err := localreview.LoadRecord(root, key)
	if err != nil {
		t.Fatal(err)
	}
	prepared.State, prepared.MergedCommit = "approved", ""
	prepared.Events = prepared.Events[:len(prepared.Events)-1]
	if err := localreview.SaveRecord(root, key, prepared); err != nil {
		t.Fatal(err)
	}
	d.markActiveEnvRoot(root)
	busyRequest := httptest.NewRequest(http.MethodPost, "/worktrees/review", bytes.NewReader(data))
	busyRequest.Header = r.Header.Clone()
	w = httptest.NewRecorder()
	d.worktreeReviewHandler()(w, busyRequest)
	d.unmarkActiveEnvRoot(root)
	if w.Code != http.StatusConflict {
		t.Fatal("recovery ignored active task environment", w.Code)
	}
	retryRequest := httptest.NewRequest(http.MethodPost, "/worktrees/review", bytes.NewReader(data))
	retryRequest.Header = r.Header.Clone()
	w = httptest.NewRecorder()
	d.worktreeReviewHandler()(w, retryRequest)
	if w.Code != http.StatusOK {
		t.Fatal("local merge did not recover", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Review.MergedCommit != mergedCommit || worktreeTestGit(t, repo, "rev-parse", "HEAD") != mergedCommit {
		t.Fatal("local retry performed another merge")
	}
	request.Action = "read"
	if err := json.Unmarshal(callReview(t, d, request, "test-token").Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	request.Action, request.SnapshotID = "approve", snapshot.ID
	if w := callReview(t, d, request, "test-token"); w.Code != http.StatusConflict {
		t.Fatal("reopened already merged source", w.Code)
	}
	request.Action = "read"
	if err := json.Unmarshal(callReview(t, d, request, "test-token").Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Review.State != "merged" || snapshot.Review.MergedCommit != mergedCommit {
		t.Fatal("rejected operation altered merge receipt")
	}
}
