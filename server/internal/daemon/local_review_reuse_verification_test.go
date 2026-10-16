package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestLocalReviewReadsVerifiedDirectoryWithoutGit(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/runtimes/runtime1/tasks/task1/review-binding" {
			http.Error(w, "unexpected binding request", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.LocalReviewRuntimeBinding{WorkspaceID: "ws1", RuntimeID: "runtime1", TaskID: "task1", AgentID: "agent1"})
	}))
	d.workspaces = map[string]*workspaceState{"ws1": {runtimeIDs: []string{"runtime1"}}}
	d.runtimeIndex = map[string]Runtime{"runtime1": {ID: "runtime1"}}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "README.txt"), []byte("plain directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := d.runRemoteReview(t.Context(), protocol.LocalReviewCommand{
		RuntimeID: "runtime1", WorkspaceID: "ws1", TaskID: "task1", Path: directory, Action: "workspace_files",
	})
	if result.Error != "" {
		t.Fatalf("non-Git workspace_files failed: %s", result.Error)
	}
	var page struct {
		Entries []struct {
			Path string `json:"path"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(result.Page, &page); err != nil {
		t.Fatalf("decode workspace files: %v; page=%s", err, result.Page)
	}
	if len(page.Entries) != 1 || page.Entries[0].Path != "README.txt" {
		t.Fatalf("workspace files = %+v, want README.txt", page.Entries)
	}
}
