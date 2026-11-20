package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestApplicationRevisionRestoreCreatesNewSnapshot(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application restore")
	app := applicationTestCreate(t, projectID, "original", "service")
	target := applicationTestCreate(t, projectID, "dependency", "service")
	config := app.Config
	config.Port = 4300
	name := "renamed"
	relations := []application.Relation{{TargetID: target.ID, Type: "depends_on", Condition: "healthy"}}
	params := map[string]string{"id": app.ID}
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+app.ID, application.UpdateInput{Revision: 1, Name: &name, Config: &config, Relations: &relations}, params)).Want(http.StatusOK)

	var restored application.View
	testutil.Call(t, testHandler.RestoreApplicationRevision, squadScopeReq("", http.MethodPost, "/applications/"+app.ID+"/restore", map[string]int64{"revision": 2, "target_revision": 1}, params)).Want(http.StatusOK).JSON(&restored)

	if restored.Revision != 3 || restored.Config.Port != 4100 || len(restored.Relations) != 0 || restored.Name != name {
		t.Fatalf("restoring config must create a revision without reverting metadata: %+v", restored)
	}
	var previous application.RevisionView
	testutil.Call(t, testHandler.GetApplicationRevision, squadScopeReq("", http.MethodGet, "/applications/"+app.ID+"/revisions/2", nil, map[string]string{"id": app.ID, "revision": "2"})).Want(http.StatusOK).JSON(&previous)
	if previous.Config.Port != 4300 || len(previous.Relations) != 1 {
		t.Fatalf("restore changed the historical snapshot: %+v", previous)
	}
}

func TestApplicationRevisionHistoryPagesWithoutRepeatingRevisions(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	app := applicationTestCreate(t, dbfx.Project(t, "application history pages"), "service", "service")
	params := map[string]string{"id": app.ID}
	for revision := int64(1); revision < 27; revision++ {
		config := app.Config
		config.Port = int(4100 + revision)
		testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+app.ID, application.UpdateInput{Revision: revision, Config: &config}, params)).Want(http.StatusOK)
	}

	var first application.RevisionPage
	testutil.Call(t, testHandler.ListApplicationRevisions, squadScopeReq("", http.MethodGet, "/applications/"+app.ID+"/revisions", nil, params)).Want(http.StatusOK).JSON(&first)
	if len(first.Revisions) != 25 || first.NextBeforeRevision == nil || *first.NextBeforeRevision != 3 {
		t.Fatalf("first page boundary: %+v", first)
	}
	for index, revision := range first.Revisions {
		if revision.ApplicationID != app.ID || revision.Revision != int64(27-index) {
			t.Fatalf("unexpected first-page revision at %d: %+v", index, revision)
		}
	}
	var last application.RevisionPage
	testutil.Call(t, testHandler.ListApplicationRevisions, squadScopeReq("", http.MethodGet, fmt.Sprintf("/applications/%s/revisions?before_revision=%d", app.ID, *first.NextBeforeRevision), nil, params)).Want(http.StatusOK).JSON(&last)
	if len(last.Revisions) != 2 || last.NextBeforeRevision != nil || last.Revisions[0].Revision != 2 || last.Revisions[1].Revision != 1 {
		t.Fatalf("cursor must exclude the previous page: %+v", last)
	}
}

func TestApplicationRevisionRestoreRejectsStaleWriterAndMissingTarget(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	app := applicationTestCreate(t, dbfx.Project(t, "application history conflicts"), "service", "service")
	params := map[string]string{"id": app.ID}
	config := app.Config
	config.Port = 4300
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+app.ID, application.UpdateInput{Revision: 1, Config: &config}, params)).Want(http.StatusOK)

	for _, scenario := range []struct {
		name   string
		body   map[string]int64
		status int
	}{
		{name: "stale writer", body: map[string]int64{"revision": 1, "target_revision": 1}, status: http.StatusConflict},
		{name: "missing target", body: map[string]int64{"revision": 2, "target_revision": 99}, status: http.StatusNotFound},
		{name: "invalid target", body: map[string]int64{"revision": 2, "target_revision": 0}, status: http.StatusBadRequest},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testutil.Call(t, testHandler.RestoreApplicationRevision, squadScopeReq("", http.MethodPost, "/applications/"+app.ID+"/restore", scenario.body, params)).Want(scenario.status)
		})
	}
	var unchanged application.View
	testutil.Call(t, testHandler.GetApplication, squadScopeReq("", http.MethodGet, "/applications/"+app.ID, nil, params)).Want(http.StatusOK).JSON(&unchanged)
	if unchanged.Revision != 2 || unchanged.Config.Port != 4300 {
		t.Fatalf("rejected restore changed the application: %+v", unchanged)
	}
}

func TestApplicationRevisionsRespectWorkspaceBoundary(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	app := applicationTestCreate(t, dbfx.Project(t, "application history isolation"), "service", "service")
	otherWorkspace := dbfx.Workspace(t, "history foreign workspace", "history-foreign-workspace")
	dbfx.Member(t, otherWorkspace, testUserID, "member")
	params := map[string]string{"id": app.ID, "revision": "1"}
	for _, scenario := range []struct {
		method  string
		path    string
		handler http.HandlerFunc
		body    map[string]int64
	}{
		{method: http.MethodGet, path: "/revisions", handler: testHandler.ListApplicationRevisions},
		{method: http.MethodGet, path: "/revisions/1", handler: testHandler.GetApplicationRevision},
		{method: http.MethodPost, path: "/restore", handler: testHandler.RestoreApplicationRevision, body: map[string]int64{"revision": 1, "target_revision": 1}},
	} {
		request := squadScopeReq("", scenario.method, "/applications/"+app.ID+scenario.path, scenario.body, params)
		request.Header.Set("X-Workspace-ID", otherWorkspace)
		testutil.Call(t, scenario.handler, request).Want(http.StatusNotFound)
	}
}
