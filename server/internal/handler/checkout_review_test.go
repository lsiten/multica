package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestCheckoutReviewProjectDescendantCarriesServerBoundRoot(t *testing.T) {
	project := dbfx.Project(t, "checkout project")
	runtime := dbfx.Runtime(t, "checkout runtime", testutil.Cols{"daemon_id": "checkout-daemon", "runtime_mode": "local", "status": "online"})
	resource := dbfx.Insert(t, "project_resource", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": project, "resource_type": "local_directory", "resource_ref": `{"local_path":"/bound","daemon_id":"checkout-daemon"}`, "created_by": testUserID})
	request := withChatTestWorkspaceCtx(t, newRequest(http.MethodPost, "/execute", map[string]string{"project_resource_id": resource, "runtime_id": runtime, "scope": "checkout", "action": "git_manifest", "git_scope": "commit", "commit_sha": strings.Repeat("a", 40), "path": "/bound/repo", "directory_root": "/"}))
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	request = request.WithContext(ctx)
	commands := make(chan protocol.LocalReviewCommand, 1)
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			command := testHandler.localReviewRelay.claim(testWorkspaceID, runtime)
			if command == nil {
				continue
			}
			commands <- *command
			testHandler.localReviewRelay.complete(testWorkspaceID, runtime, command.ID, protocol.LocalReviewResult{ClaimToken: command.ClaimToken, Page: []byte(`{"version_id":"captured"}`)})
			return
		}
	}()
	var response protocol.LocalReviewResult
	testutil.Call(t, testHandler.ForwardLocalReview, request).Want(http.StatusOK).JSON(&response)
	select {
	case command := <-commands:
		if command.DirectoryRoot != "/bound" || command.Path != "/bound/repo" || command.TaskID != "" || command.RuntimeID != runtime || command.GitScope != "commit" || command.CommitSHA != strings.Repeat("a", 40) {
			t.Fatalf("wrong authorized checkout command: %+v", command)
		}
	case <-ctx.Done():
		t.Fatal("authorized commit read did not reach relay")
	}
}

func TestCheckoutReviewRejectsEscapesAndMutations(t *testing.T) {
	project := dbfx.Project(t, "bounded checkout")
	runtime := dbfx.Runtime(t, "bounded runtime", testutil.Cols{"daemon_id": "bounded-daemon", "runtime_mode": "local", "status": "online"})
	resource := dbfx.Insert(t, "project_resource", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": project, "resource_type": "local_directory", "resource_ref": `{"local_path":"/bound","daemon_id":"bounded-daemon"}`, "created_by": testUserID})
	for _, tc := range []struct{ path, action string }{{"/bound/../outside", "index"}, {"/boundary/repo", "index"}, {"/bound/repo", "stage"}, {"/bound/repo", "merge"}} {
		req := withChatTestWorkspaceCtx(t, newRequest(http.MethodPost, "/execute", map[string]string{"project_resource_id": resource, "runtime_id": runtime, "scope": "checkout", "action": tc.action, "path": tc.path}))
		testutil.Call(t, testHandler.ForwardLocalReview, req).Want(http.StatusBadRequest)
	}
}
