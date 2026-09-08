package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestLocalReviewForwardRejectsUnauthorizedActions(t *testing.T) {
	member := dbfx.User(t, "MR Member", "mr-member@example.test")
	dbfx.Member(t, testWorkspaceID, member, "member")
	publicRuntime := dbfx.Runtime(t, "public-review", testutil.Cols{"runtime_mode": "local", "visibility": "public"})
	privateRuntime := dbfx.Runtime(t, "private-review", testutil.Cols{"runtime_mode": "local"})
	publicAgent := dbfx.Agent(t, "public-agent", publicRuntime)
	privateAgent := dbfx.Agent(t, "private-agent", privateRuntime)
	issue := dbfx.Issue(t, "Review authorization")
	publicTask := dbfx.Task(t, publicAgent, testutil.Cols{"runtime_id": publicRuntime, "issue_id": issue, "work_dir": "/runtime/repo", "status": "completed"})
	privateTask := dbfx.Task(t, privateAgent, testutil.Cols{"runtime_id": privateRuntime, "issue_id": issue, "work_dir": "/runtime/repo", "status": "completed"})
	personalTask := dbfx.Task(t, publicAgent, testutil.Cols{"runtime_id": publicRuntime, "work_dir": "/runtime/repo", "status": "completed"})
	for _, tc := range []struct {
		name, user, actor, task, action, path string
		status                                int
	}{
		{"anonymous", "", "", publicTask, "merge", "/runtime/repo", http.StatusUnauthorized},
		{"task token", testUserID, "task_token", publicTask, "merge", "/runtime/repo", http.StatusForbidden},
		{"cloud PAT", testUserID, "cloud_pat", publicTask, "approve", "/runtime/repo", http.StatusForbidden},
		{"nonowner merge", member, "", publicTask, "merge", "/runtime/repo", http.StatusForbidden},
		{"private runtime", member, "", privateTask, "read", "/runtime/repo", http.StatusNotFound},
		{"personal run", member, "", personalTask, "read", "/runtime/repo", http.StatusForbidden},
		{"foreign directory", testUserID, "", publicTask, "read", "/other/repo", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRequestAsUser(tc.user, http.MethodPost, "/api/local-reviews/execute", map[string]string{
				"task_id": tc.task, "path": tc.path, "target": "main", "action": tc.action, "snapshot_id": "snapshot", "command_id": "test-operation",
			})
			r.Header.Set("X-Actor-Source", tc.actor)
			var handler http.Handler = http.HandlerFunc(testHandler.ForwardLocalReview)
			if tc.user != "" {
				handler = middleware.RequireWorkspaceMember(testHandler.Queries)(handler)
			}
			testutil.Call(t, handler.ServeHTTP, r).Want(tc.status)
			if len(testHandler.localReviewRelay.pending) != 0 {
				t.Fatal("rejected request reached runtime relay")
			}
		})
	}
}

func TestLocalReviewClaimRequiresOwningCredential(t *testing.T) {
	member := dbfx.User(t, "MR Claim Member", "mr-claim-member@example.test")
	dbfx.Member(t, testWorkspaceID, member, "member")
	runtime := dbfx.Runtime(t, "claim-owner", testutil.Cols{"runtime_mode": "local", "visibility": "public", "daemon_id": "owning-daemon"})
	for _, tc := range []struct {
		name, user, daemon string
		status             int
	}{
		{"owner PAT", testUserID, "", http.StatusOK},
		{"other member PAT", member, "", http.StatusForbidden},
		{"owning daemon", "", "owning-daemon", http.StatusOK},
		{"other daemon", "", "other-daemon", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRequestAsUser(tc.user, http.MethodPost, "/claim", nil)
			if tc.daemon != "" {
				r = newDaemonTokenRequest(http.MethodPost, "/claim", nil, testWorkspaceID, tc.daemon)
			}
			r = testutil.WithURLParams(r, "runtimeId", runtime)
			testutil.Call(t, testHandler.ClaimLocalReviewRelay, r).Want(tc.status)
		})
	}
}

func TestLocalReviewRuntimeBindingIsOwnerAndTaskScoped(t *testing.T) {
	runtime := dbfx.Runtime(t, "binding-runtime", testutil.Cols{"runtime_mode": "local", "visibility": "public"})
	otherRuntime := dbfx.Runtime(t, "other-binding-runtime")
	agent := dbfx.Agent(t, "binding-agent", runtime)
	task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime})
	member := dbfx.User(t, "Binding Reader", "binding-reader@example.test")
	dbfx.Member(t, testWorkspaceID, member, "member")
	for _, tc := range []struct {
		name, user, runtime string
		status              int
	}{
		{"owner", testUserID, runtime, http.StatusOK},
		{"other member", member, runtime, http.StatusForbidden},
		{"wrong runtime", testUserID, otherRuntime, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRequestAsUser(tc.user, http.MethodGet, "/review-binding", nil)
			r = testutil.WithURLParams(r, "runtimeId", tc.runtime, "taskId", task)
			response := testutil.Call(t, testHandler.GetLocalReviewRuntimeBinding, r).Want(tc.status)
			if tc.status == http.StatusOK {
				var binding protocol.LocalReviewRuntimeBinding
				response.JSON(&binding)
				if binding.TaskID != task || binding.RuntimeID != runtime || binding.WorkspaceID != testWorkspaceID || binding.AgentID != agent {
					t.Fatal("incorrect binding", binding)
				}
			}
			if tc.name != "wrong runtime" {
				request := newRequestAsUser(tc.user, http.MethodGet, "/discover-review-binding", nil)
				request = testutil.WithURLParams(request, "taskId", task)
				result := testutil.Call(t, testHandler.DiscoverLocalReviewRuntimeBinding, request).Want(tc.status)
				if tc.status == http.StatusOK {
					var binding protocol.LocalReviewRuntimeBinding
					result.JSON(&binding)
					if binding.RuntimeID != runtime || binding.TaskID != task {
						t.Fatal("discovery guessed runtime", binding)
					}
				}
			}
		})
	}
}
