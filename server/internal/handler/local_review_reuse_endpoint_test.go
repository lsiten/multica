package handler

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestReviewReuseBindingEndpoint(t *testing.T) {
	// Given two runs sharing a directory and a different issue using that path.
	runtime := dbfx.Runtime(t, "reuse-runtime", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "reuse-agent", runtime)
	issue := dbfx.Issue(t, "Reused directory")
	original := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "work_dir": "/runtime/workdir", "status": "completed"})
	current := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "work_dir": "/runtime/workdir", "status": "completed"})
	other := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": dbfx.Issue(t, "Other issue"), "work_dir": "/runtime/workdir"})
	for _, tc := range []struct {
		name, owner, path string
		status            int
	}{
		{"verified", original, "/runtime/workdir/repo", http.StatusOK},
		{"other issue", other, "/runtime/workdir/repo", http.StatusForbidden},
		{"outside", original, "/runtime/private", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// When the runtime asks for server-verified ownership rather than guessing.
			query := url.Values{"directory_task_id": {tc.owner}, "path": {tc.path}}
			r := newRequestAsUser(testUserID, http.MethodGet, "/review-binding?"+query.Encode(), nil)
			r = testutil.WithURLParams(r, "runtimeId", runtime, "taskId", current)
			result := testutil.Call(t, testHandler.GetLocalReviewRuntimeBinding, r).Want(tc.status)
			// Then only proven reuse returns the original review identity.
			if tc.status == http.StatusOK {
				var binding protocol.LocalReviewRuntimeBinding
				result.JSON(&binding)
				if binding.TaskID != original || binding.RuntimeID != runtime {
					t.Fatal("incorrect directory identity", binding)
				}
			}
		})
	}
}
