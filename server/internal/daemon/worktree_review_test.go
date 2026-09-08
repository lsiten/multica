package daemon

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalReviewReadsPinnedDeliveryAfterWorktreeRemoval(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	if err := execenv.WriteReviewRuntime(root, execenv.ReviewRuntime{WorkspaceID: "ws1", TaskID: "task1", RuntimeID: "runtime1"}); err != nil {
		t.Fatal(err)
	}
	repo := createWorktreeTestRepo(t)
	checkout := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", checkout)
	if err := os.WriteFile(filepath.Join(checkout, "app.txt"), []byte("delivery\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, checkout, "add", "app.txt")
	worktreeTestGit(t, checkout, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-m", "delivery")
	head := worktreeTestGit(t, checkout, "rev-parse", "HEAD")
	if err := execenv.WriteReviewDirectory(root, execenv.ReviewDirectory{WorkspaceID: "ws1", TaskID: "task1", Path: repo, SourcePath: checkout, Branch: "feature", Commit: head}); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, repo, "worktree", "remove", checkout)
	if err := os.WriteFile(filepath.Join(repo, "private.txt"), []byte("later private file"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := worktreeReviewRequest{TaskID: "task1", WorkspaceID: "ws1", Path: checkout, Target: "main"}
	w := callReview(t, d, request, "test-token")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var snapshot worktreeReviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if !snapshot.Committed || snapshot.Head != head || len(snapshot.Files) != 1 || snapshot.Files[0].Path != "app.txt" {
		t.Fatal("did not read pinned delivery")
	}
	if _, removed := d.cleanTaskDir(root); !removed {
		t.Fatal("task directory was not archived and removed")
	}
	runtime, err := execenv.ReadReviewRuntime(execenv.ReviewArchivePath(d.cfg.WorkspacesRoot, "ws1", "task1"))
	if err != nil || runtime.RuntimeID != "runtime1" || runtime.TaskID != "task1" {
		t.Fatal("archived runtime binding lost", runtime, err)
	}
	w = callReview(t, d, request, "test-token")
	if w.Code != http.StatusOK {
		t.Fatal("archived review unavailable", w.Code, w.Body.String())
	}
	var archived worktreeReviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &archived); err != nil {
		t.Fatal(err)
	}
	if archived.ID != snapshot.ID || archived.Head != head {
		t.Fatal("archived review changed its source")
	}
	request.TaskID = "other-task"
	if w := callReview(t, d, request, "test-token"); w.Code != http.StatusForbidden {
		t.Fatal("accepted wrong task binding")
	}
}

func TestLocalReviewExternalDirectoryRequiresPreparedBinding(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", &execenv.GCMeta{LocalDirectory: true})
	repo := createWorktreeTestRepo(t)
	request := worktreeReviewRequest{WorkspaceID: "ws1", TaskID: "task1", Path: repo, Target: "main"}
	if _, _, err := d.resolveReviewRoot(context.Background(), request); err == nil {
		t.Fatal("accepted unbound external directory")
	}
	if err := execenv.WriteReviewDirectory(root, execenv.ReviewDirectory{WorkspaceID: "ws1", TaskID: "task1", Path: repo}); err != nil {
		t.Fatal(err)
	}
	resolved, _, err := d.resolveReviewRoot(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(repo)
	if err != nil || resolved != canonical {
		t.Fatal("wrong bound directory", err)
	}
	request.TaskID = "other-task"
	if _, _, err := d.resolveReviewRoot(context.Background(), request); err == nil {
		t.Fatal("binding accepted for another task")
	}
	request.TaskID, request.Path = "task1", t.TempDir()
	if _, _, err := d.resolveReviewRoot(context.Background(), request); err == nil {
		t.Fatal("binding accepted for another directory")
	}
}

func callReview(t *testing.T, d *Daemon, request worktreeReviewRequest, token string) *httptest.ResponseRecorder {
	t.Helper()
	if request.Action != "" && request.Action != "read" && request.CommandID == "" {
		request.CommandID = uuid.NewString()
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/worktrees/review", strings.NewReader(string(data)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Multica-Profile", d.cfg.Profile)
	w := httptest.NewRecorder()
	d.reviewOperationHandler(true)(w, r)
	return w
}

func TestLocalReviewLoopbackRequiresRuntimeIdentityForDecisions(t *testing.T) {
	d := worktreeTestDaemon(t)
	for _, action := range []string{"submit", "approve", "request_changes", "merge"} {
		body, err := json.Marshal(worktreeReviewRequest{TaskID: "task1", WorkspaceID: "ws1", Path: t.TempDir(), Target: "main", Action: action})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/worktrees/review", strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer test-token")
		r.Header.Set("X-Multica-Profile", d.cfg.Profile)
		w := httptest.NewRecorder()
		d.worktreeReviewHandler()(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("loopback accepted %s: %d", action, w.Code)
		}
	}
}

func TestLocalReviewRequiresOwnershipAndCurrentApproval(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	worktreeTestGit(t, repo, "config", "user.name", "Review")
	worktreeTestGit(t, repo, "config", "user.email", "review@example.test")
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	worktreeTestGit(t, path, "commit", "--allow-empty", "-m", "feature")
	request := worktreeReviewRequest{TaskID: "task1", WorkspaceID: "ws1", Path: path, Target: "main"}
	if w := callReview(t, d, request, "wrong"); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
	wrong := request
	wrong.TaskID = "another-task"
	if w := callReview(t, d, wrong, "test-token"); w.Code != http.StatusForbidden {
		t.Fatal(w.Code, w.Body.String())
	}
	w := callReview(t, d, request, "test-token")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var snapshot worktreeReviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	request.SnapshotID, request.Action = snapshot.ID, "merge"
	if w := callReview(t, d, request, "test-token"); w.Code != http.StatusConflict {
		t.Fatal("unapproved merge", w.Code)
	}
	request.Action = "approve"
	if w := callReview(t, d, request, "test-token"); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	worktreeTestGit(t, path, "commit", "--allow-empty", "-m", "changed after approval")
	request.Action = "merge"
	if w := callReview(t, d, request, "test-token"); w.Code != http.StatusConflict {
		t.Fatal("stale merge", w.Code)
	}
	request.Action = "read"
	w = callReview(t, d, request, "test-token")
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Review.State != "draft" {
		t.Fatal("approval survived source update")
	}
	request.SnapshotID, request.Action = snapshot.ID, "approve"
	if w := callReview(t, d, request, "test-token"); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	request.Action = "merge"
	w = callReview(t, d, request, "test-token")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	request.Action = "read"
	w = callReview(t, d, request, "test-token")
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Review.State != "merged" {
		t.Fatal("merged state lost on reload", w.Body.String())
	}
}
