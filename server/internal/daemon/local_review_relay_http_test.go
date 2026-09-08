package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestLocalReviewLoopUsesTransientRelayHTTP(t *testing.T) {
	// Given a real HTTP transport and a task-owned Git worktree.
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	results := make(chan protocol.LocalReviewResult, 1)
	var command protocol.LocalReviewCommand
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/daemon/runtimes/runtime/local-reviews/relay/claim":
			if err := json.NewEncoder(w).Encode(protocol.LocalReviewClaim{Command: &command}); err != nil {
				t.Error(err)
			}
		case "/api/daemon/runtimes/runtime/local-reviews/relay/request/result":
			var result protocol.LocalReviewResult
			if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
				t.Error(err)
			}
			results <- result
			cancel()
		default:
			t.Errorf("unexpected durable queue or API call: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			cancel()
		}
	}))
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	d.workspaces = map[string]*workspaceState{"ws1": {runtimeIDs: []string{"runtime"}}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	command = protocol.LocalReviewCommand{ID: "request", ClaimToken: "claim", RuntimeID: "runtime", WorkspaceID: "ws1", TaskID: "task1", Path: path, Target: "main", Action: "read"}
	// When the daemon runs its actual forwarding consumer.
	d.localReviewLoop(ctx)
	// Then both the Git snapshot and runtime-owned MR record return over HTTP.
	select {
	case result := <-results:
		if result.Error != "" || result.ClaimToken != "claim" || len(result.Snapshot) == 0 || len(result.Review) == 0 {
			t.Fatalf("unexpected relay result: %+v", result)
		}
	default:
		t.Fatal("daemon did not return a relay response")
	}
}
