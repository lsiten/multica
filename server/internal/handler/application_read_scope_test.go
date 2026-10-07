package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestApplicationAgentReadScopeCoversCatalogBoardAndDetails(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	runtimeID := applicationTestRuntime(t)
	agentID := dbfx.Agent(t, "application reader", runtimeID)
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID})
	allowedProject := dbfx.Project(t, "allowed application read", testutil.Cols{"lead_type": "agent", "lead_id": agentID})
	deniedProject := dbfx.Project(t, "denied application read")
	allowed := applicationTestCreate(t, allowedProject, "allowed", "service")
	denied := applicationTestCreate(t, deniedProject, "restricted", "service")
	applicationTestOperationCleanup(t, allowed.ID)
	applicationTestOperationCleanup(t, denied.ID)
	applicationTestEnqueue(t, denied, runtimeID, "start", "restricted-observation")
	applicationTestFinish(t, runtimeID, applicationTestClaim(t, runtimeID)[0], "completed")
	request := func(resourceID string) *http.Request {
		r := squadScopeReq("", http.MethodGet, "/applications", nil, map[string]string{"id": resourceID})
		r.Header.Set("X-Actor-Source", "task_token")
		r.Header.Set("X-Agent-ID", agentID)
		r.Header.Set("X-Task-ID", taskID)
		return r
	}
	var list struct{ Applications []application.View }
	testutil.Call(t, testHandler.ListApplications, request("")).Want(http.StatusOK).JSON(&list)
	for _, app := range list.Applications {
		if app.ID == denied.ID {
			t.Error("project-scoped agent read another project's application configuration")
		}
	}
	var board applicationBoardView
	testutil.Call(t, testHandler.GetApplicationBoard, request("")).Want(http.StatusOK).JSON(&board)
	for _, app := range board.Applications {
		if app.ID == denied.ID {
			t.Error("board leaked restricted application")
		}
	}
	for _, instance := range board.Instances {
		if instance.ApplicationID == denied.ID {
			t.Error("board leaked restricted instance")
		}
	}
	for _, operation := range board.Operations {
		if operation.ApplicationID == denied.ID {
			t.Error("board leaked restricted operation")
		}
	}
	testutil.Call(t, testHandler.GetApplication, request(denied.ID)).Want(http.StatusForbidden)
	testutil.Call(t, testHandler.GetApplication, request(allowed.ID)).Want(http.StatusOK)
}
