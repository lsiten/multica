package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationConnectionPlanFreezesSelectedDependencyAndLocalAddress(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application connection plan")
	target := applicationTestCreate(t, projectID, "API", "service")
	resourceID := dbfx.Insert(t, "project_resource", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": projectID, "resource_type": "github_repo", "resource_ref": testutil.Raw(`'{"url":"https://github.com/example/application"}'::jsonb`)})
	config := protocol.DefaultApplicationConfig()
	config.Port = 4200
	config.ResourceID = resourceID
	config.Command = []string{"test-created-application-command"}
	config.Connections = []protocol.ApplicationConnection{{TargetID: target.ID, URLVariable: "API_URL"}}
	var source application.View
	testutil.Call(t, testHandler.CreateApplication, squadScopeReq("", http.MethodPost, "/applications", map[string]any{"project_id": projectID, "name": "frontend", "kind": "service", "config": config}, nil)).Want(http.StatusCreated).JSON(&source)
	dbfx.Cleanup(t, "DELETE FROM application WHERE id=$1", source.ID)
	dbfx.Cleanup(t, "DELETE FROM application_revision WHERE application_id=$1", source.ID)
	dbfx.Cleanup(t, "DELETE FROM application_relation WHERE source_id=$1", source.ID)
	dbfx.Cleanup(t, "DELETE FROM application_instance WHERE application_id=$1", source.ID)
	applicationTestOperationCleanup(t, source.ID)
	applicationTestOperationCleanup(t, target.ID)
	relations := []application.Relation{{TargetID: target.ID, Type: "depends_on", Condition: "healthy"}}
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+source.ID, application.UpdateInput{Revision: source.Revision, Relations: &relations}, map[string]string{"id": source.ID})).Want(http.StatusOK).JSON(&source)
	runtimeID := applicationTestRuntime(t)
	applicationTestEnqueue(t, target, runtimeID, "start", "connection-target")
	applicationTestFinish(t, runtimeID, applicationTestClaim(t, runtimeID)[0], "completed")
	applicationTestEnqueue(t, source, runtimeID, "start", "connection-source")
	claims := applicationTestClaim(t, runtimeID)
	if len(claims) != 1 || len(claims[0].Command.Connections) != 1 {
		t.Fatalf("connection missing from execution: %+v", claims)
	}
	binding := claims[0].Command.Connections[0]
	if claims[0].Command.AccessMemberID != applicationTestMemberID(t, testUserID) {
		t.Fatal("connection did not freeze the initiating membership")
	}
	if binding.LocalURL != "http://127.0.0.1:4100/" || binding.Grant != "" || binding.TargetGeneration != 1 || binding.TargetInstanceID != claims[0].Command.Dependencies[0].InstanceID {
		t.Fatalf("selected local dependency changed: %+v", binding)
	}
}
