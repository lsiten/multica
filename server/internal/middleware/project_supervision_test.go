package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCoordinationTaskTokenFencesWrites(t *testing.T) {
	pool := openPool(t)
	t.Cleanup(pool.Close)
	f := testutil.New(pool, "", "")
	user := f.User(t, "coordination token", "coordination-token@multica.test")
	workspace := f.Workspace(t, "coordination token", "coordination-token")
	f.WorkspaceID = workspace
	f.UserID = user
	f.Member(t, workspace, user, "owner")
	rt := f.Runtime(t, "runtime")
	agent := f.Agent(t, "agent", rt)
	project := f.Project(t, "project")
	task := f.Task(t, agent, testutil.Cols{"status": "running", "runtime_id": rt, "context": testutil.Raw(fmt.Sprintf(`'{"type":"project_supervision","project_id":"%s","workspace_id":"%s"}'::jsonb`, project, workspace))})
	const token = "mat_coordination-test"
	f.Insert(t, "task_token", testutil.Cols{"token_hash": auth.HashToken(token), "task_id": task, "agent_id": agent, "workspace_id": workspace, "user_id": user, "expires_at": testutil.Raw("now()+interval '1 hour'")})
	middleware := Auth(db.New(pool), nil, nil, nil)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Task-ID") != task || r.Header.Get("X-Agent-ID") != agent {
			t.Error("task identity spoofed")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, input := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/projects/" + project + "/supervision", 204},
		{"POST", "/api/projects/" + project + "/supervision/actions", 204},
		{"POST", "/api/projects/" + project + "/supervision/report", 204},
		{"POST", "/api/projects/other/supervision/actions", 403},
		{"PUT", "/api/projects/" + project + "/supervision", 403},
		{"POST", "/api/issues", 403},
		{"PUT", "/api/issues/any", 403},
	} {
		r := httptest.NewRequest(input.method, input.path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Task-ID", "spoof")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != input.status {
			t.Fatalf("%s %s: %d %s", input.method, input.path, w.Code, w.Body.String())
		}
	}
	f.Exec(t, "UPDATE agent_task_queue SET status='completed' WHERE id=$1", task)
	r := httptest.NewRequest("POST", "/api/projects/"+project+"/supervision/actions", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("terminal task still writes: %d", w.Code)
	}
}
