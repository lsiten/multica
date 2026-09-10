package handler

import (
	"github.com/multica-ai/multica/server/internal/middleware"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestIndexWritesRequireRuntimeOwner(t *testing.T) {
	member := dbfx.User(t, "Index Reader", "index-reader@example.test")
	dbfx.Member(t, testWorkspaceID, member, "member")
	runtime := dbfx.Runtime(t, "index-public", testutil.Cols{"runtime_mode": "local", "visibility": "public"})
	agent := dbfx.Agent(t, "index-agent", runtime)
	issue := dbfx.Issue(t, "Index authorization")
	task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "work_dir": "/runtime/repo", "status": "completed"})
	for _, action := range []string{"stage", "unstage", "commit", "merge_selected"} {
		t.Run(action, func(t *testing.T) {
			input := protocol.LocalReviewCommand{TaskID: task, Path: "/runtime/repo", Target: "main", Action: action, CommandID: "operation", VersionID: strings.Repeat("a", 64), SnapshotID: strings.Repeat("a", 64), IndexID: strings.Repeat("b", 64), Head: strings.Repeat("c", 40), Branch: "feature", Paths: []string{"app.txt"}, Message: "commit message"}
			r := newRequestAsUser(member, http.MethodPost, "/api/local-reviews/execute", input)
			handler := middleware.RequireWorkspaceMember(testHandler.Queries)(http.HandlerFunc(testHandler.ForwardLocalReview))
			testutil.Call(t, handler.ServeHTTP, r).Want(http.StatusForbidden)
			if len(testHandler.localReviewRelay.pending) != 0 {
				t.Fatal("unauthorized Git write reached the runtime")
			}
		})
	}
}
