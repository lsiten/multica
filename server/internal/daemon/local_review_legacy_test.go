package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestLocalReviewLegacyBindingRequiresVerifiedRuntime(t *testing.T) {
	for _, matched := range []bool{true, false} {
		t.Run(map[bool]string{true: "verified", false: "wrong runtime"}[matched], func(t *testing.T) {
			var calls atomic.Int32
			d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/daemon/runtimes/runtime1/tasks/task1/review-binding" {
					http.NotFound(w, r)
					return
				}
				calls.Add(1)
				runtimeID := "runtime1"
				if !matched {
					runtimeID = "other-runtime"
				}
				if err := json.NewEncoder(w).Encode(protocol.LocalReviewRuntimeBinding{WorkspaceID: "ws1", TaskID: "task1", RuntimeID: runtimeID, AgentID: "agent1"}); err != nil {
					t.Error(err)
				}
			}))
			d.workspaces = map[string]*workspaceState{"ws1": {runtimeIDs: []string{"runtime1"}}}
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
			repo := createWorktreeTestRepo(t)
			path := filepath.Join(root, "workdir", "repo")
			worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
			request := worktreeReviewRequest{WorkspaceID: "ws1", TaskID: "task1", RuntimeID: "runtime1", Path: path, Target: "main"}
			var snapshot worktreeReviewResponse
			if err := json.Unmarshal(callReview(t, d, request, "test-token").Body.Bytes(), &snapshot); err != nil {
				t.Fatal(err)
			}
			request.Action, request.SnapshotID, request.CommandID = "approve", snapshot.ID, "approve-legacy"
			body, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			invoke := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, "/worktrees/review", bytes.NewReader(body))
				r.Header.Set("Authorization", "Bearer test-token")
				r.Header.Set("X-Multica-Profile", d.cfg.Profile)
				w := httptest.NewRecorder()
				d.worktreeReviewHandler()(w, r)
				return w
			}
			response := invoke()
			binding, err := execenv.ReadReviewRuntime(root)
			if !matched {
				if response.Code != http.StatusForbidden || !os.IsNotExist(err) {
					t.Fatal("unverified binding accepted", response.Code, err)
				}
				return
			}
			if response.Code != http.StatusOK || err != nil || binding.RuntimeID != "runtime1" || binding.AgentID != "agent1" {
				t.Fatal("verified binding not persisted", response.Code, binding, err)
			}
			if response := invoke(); response.Code != http.StatusOK {
				t.Fatal(response.Code, response.Body.String())
			}
			if calls.Load() != 1 {
				t.Fatal("local retry repeated cloud verification", calls.Load())
			}
		})
	}
}
