package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestReviewReusedDirectoryRequiresServerProof(t *testing.T) {
	for _, remote := range []bool{false, true} {
		for _, permitted := range []bool{false, true} {
			t.Run(fmtReviewReuseCase(remote, permitted), func(t *testing.T) {
				// Given a continuation whose directory is owned by an earlier run.
				d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !permitted || r.URL.Path != "/api/daemon/runtimes/runtime1/tasks/continuation/review-binding" || r.URL.Query().Get("directory_task_id") != "original" {
						http.Error(w, "unverified reuse", http.StatusForbidden)
						return
					}
					if err := json.NewEncoder(w).Encode(protocol.LocalReviewRuntimeBinding{WorkspaceID: "ws1", RuntimeID: "runtime1", TaskID: "original", AgentID: "agent1"}); err != nil {
						t.Error(err)
					}
				}))
				d.workspaces = map[string]*workspaceState{"ws1": {runtimeIDs: []string{"runtime1"}}}
				d.runtimeIndex = map[string]Runtime{"runtime1": {ID: "runtime1"}}
				root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "original", nil)
				repo := createWorktreeTestRepo(t)
				checkout := filepath.Join(root, "workdir", "repo")
				worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", checkout)
				// When reading the repository through either transport.
				var succeeded bool
				if remote {
					result := d.runRemoteReview(t.Context(), protocol.LocalReviewCommand{RuntimeID: "runtime1", WorkspaceID: "ws1", TaskID: "continuation", Path: checkout, Action: "branches"})
					succeeded = result.Error == "" && len(result.Branches) > 0
				} else {
					body, err := json.Marshal(worktreeReviewRequest{RuntimeID: "runtime1", WorkspaceID: "ws1", TaskID: "continuation", Path: checkout, Action: "branches"})
					if err != nil {
						t.Fatal(err)
					}
					r := httptest.NewRequest(http.MethodPost, "/worktrees/review", bytes.NewReader(body))
					r.Header.Set("Authorization", "Bearer test-token")
					r.Header.Set("X-Multica-Profile", d.cfg.Profile)
					result := httptest.NewRecorder()
					d.worktreeReviewHandler()(result, r)
					succeeded = result.Code == http.StatusOK
				}
				// Then only independently verified reuse is accepted.
				if succeeded != permitted {
					t.Fatalf("reuse accepted=%v, want %v", succeeded, permitted)
				}
			})
		}
	}
}

func fmtReviewReuseCase(remote, permitted bool) string {
	transport, proof := "local", "denied"
	if remote {
		transport = "remote"
	}
	if permitted {
		proof = "verified"
	}
	return transport + "/" + proof
}
