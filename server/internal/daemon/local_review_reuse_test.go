package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestLocalReviewResolvesReusedTaskBinding(t *testing.T) {
	d := worktreeTestDaemon(t)
	priorRoot := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "prior1", nil)
	currentRoot := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "current1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(priorRoot, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	if err := execenv.WriteReviewDirectory(currentRoot, execenv.ReviewDirectory{WorkspaceID: "ws1", TaskID: "current1", Path: path}); err != nil {
		t.Fatal(err)
	}
	request := worktreeReviewRequest{WorkspaceID: "ws1", TaskID: "current1", Path: path, Target: "main"}
	resolved, recordRoot, err := d.resolveReviewRoot(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != canonical || recordRoot != currentRoot {
		t.Fatal("wrong reused task binding", resolved, recordRoot, err)
	}
	rows, err := d.managedWorktrees(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, row := range rows {
		if row.TaskID == "current1" && len(row.Repositories) == 1 && row.Repositories[0] == canonical {
			listed = true
		}
	}
	if !listed {
		t.Fatal("cleanup inventory omitted reused repository")
	}
	owner, err := d.gcTaskDirOwner(priorRoot)
	if err != nil || owner.TaskID != "prior1" {
		t.Fatal("prior task ownership changed", owner, err)
	}
	var snapshot worktreeReviewResponse
	if err := json.Unmarshal(callReview(t, d, request, "test-token").Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	request.Action, request.SnapshotID = "approve", snapshot.ID
	d.markActiveEnvRoot(priorRoot)
	w := callReview(t, d, request, "test-token")
	d.unmarkActiveEnvRoot(priorRoot)
	if w.Code != http.StatusConflict {
		t.Fatal("review ignored an active reused source", w.Code)
	}
	if w := callReview(t, d, request, "test-token"); w.Code != http.StatusOK {
		t.Fatal("idle reused source rejected", w.Code, w.Body.String())
	}
	releaseSource, err := d.claimReviewSource(context.Background(), canonical, currentRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSource()
	if releaseGC, ok := d.reserveEnvRootForGC(priorRoot); ok {
		releaseGC()
		t.Fatal("source reservation allowed concurrent cleanup")
	}
	workspace, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	relative, err := filepath.Rel(d.cfg.WorkspacesRoot, priorRoot)
	if err != nil {
		t.Fatal(err)
	}
	claim, _, err := execenv.LockEnvRootForReuse(workspace, relative, priorRoot)
	if err == nil && claim != nil {
		claim.Release()
		t.Fatal("source reservation allowed another environment claim")
	}
	request.TaskID = "unrelated"
	if _, _, err := d.resolveReviewRoot(context.Background(), request); err == nil {
		t.Fatal("unbound task accepted reused worktree")
	}
}
