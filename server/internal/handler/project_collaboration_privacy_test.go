package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestReviewRawGraphEventsRespectPrivateChatVisibility(t *testing.T) {
	project := dbfx.Project(t, "review-private-events")
	runtime := dbfx.Runtime(t, "graph-review-runtime")
	agent := dbfx.Agent(t, "private-chat-agent", runtime)
	session := createHandlerTestChatSession(t, agent)
	dbfx.Exec(t, `UPDATE chat_session SET project_id=$1 WHERE id=$2`, project, session)
	task := dbfx.Task(t, agent, testutil.Cols{"chat_session_id": session, "status": "completed", "runtime_id": runtime})
	dbfx.Insert(t, "project_graph_event", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": project, "task_id": task, "event_type": "task_finished", "node_id": task, "data": testutil.Raw(`'{"provider":"fixture"}'::jsonb`)})
	viewer := dbfx.User(t, "review-member", "graph-review-member@example.test")
	dbfx.Member(t, testWorkspaceID, viewer, "member")
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{UserID: parseUUID(viewer), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	call := func(handler http.HandlerFunc) *httptest.ResponseRecorder {
		r := withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/graph/events", nil), "id", project)
		r.Header.Set("X-User-ID", viewer)
		r = r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, member))
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		return w
	}
	graph := call(testHandler.GetProjectCollaborationGraph)
	var g struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(graph.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 0 {
		t.Fatal("fixture must be hidden in aggregate graph")
	}
	events := call(testHandler.ListProjectGraphEvents)
	var response struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(events.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 0 {
		t.Fatalf("private chat hidden by aggregate graph but raw endpoint exposed %d event(s)", len(response.Events))
	}
}

func TestReviewWaitingDirectoryCountsAgree(t *testing.T) {
	child, parent := graphTestUUID(1), graphTestUUID(2)
	graph := buildProjectCollaboration(projectCollaborationDataset{Runs: []db.ListProjectCollaborationRunsRow{{IssueID: graphTestUUID(5), AgentID: child, AgentName: "child", SourceAgentID: parent, SourceAgentName: "parent", Status: "waiting_local_directory", TaskActive: true, RelationType: "delegated"}}})
	if len(graph.Edges) != 1 || graph.Edges[0].ActiveCount != 1 {
		t.Fatal("fixture edge is not active")
	}
	if graph.Summary.ActiveCount != 1 {
		t.Fatalf("edge active=1 but summary active=%d", graph.Summary.ActiveCount)
	}
}

func TestProjectCollaborationActivityUsesUnfinishedIssueState(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	project := dbfx.Project(t, "activity-by-issue-state")
	runtime := dbfx.Runtime(t, "activity-by-issue-runtime")
	agent := dbfx.Agent(t, "activity-by-issue-agent", runtime)
	issue := dbfx.Issue(t, "unfinished project task", testutil.Cols{"project_id": project, "status": "in_review", "assignee_type": "agent", "assignee_id": agent})
	dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "completed"})
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{UserID: parseUUID(testUserID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	call := func(activity string) struct {
		TaskCount int `json:"task_count"`
	} {
		r := withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/collaboration-graph?activity="+activity, nil), "id", project)
		r.Header.Set("X-User-ID", testUserID)
		r = r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, member))
		w := httptest.NewRecorder()
		testHandler.GetProjectCollaborationGraph(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("activity=%s status=%d body=%s", activity, w.Code, w.Body.String())
		}
		var response struct {
			Summary struct {
				TaskCount int `json:"task_count"`
			} `json:"summary"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Summary
	}
	if got := call("active").TaskCount; got != 1 {
		t.Fatalf("active unfinished issue task count=%d, want 1", got)
	}
	if got := call("ended").TaskCount; got != 0 {
		t.Fatalf("ended unfinished issue task count=%d, want 0", got)
	}
}

func TestProjectRawGraphEventsHideSourceAndKeepVisiblePage(t *testing.T) {
	project := dbfx.Project(t, "raw-graph-visible-page")
	runtime := dbfx.Runtime(t, "raw-graph-runtime")
	hiddenAgent := dbfx.Agent(t, "hidden-source", runtime)
	issue := dbfx.Issue(t, "graph issue", testutil.Cols{"project_id": project})
	hiddenTask := dbfx.Task(t, hiddenAgent, testutil.Cols{"issue_id": issue, "runtime_id": runtime})
	viewer := dbfx.User(t, "graph-reader", "graph-reader@example.test")
	dbfx.Member(t, testWorkspaceID, viewer, "member")
	visibleAgent := dbfx.Agent(t, "visible-child", runtime, testutil.Cols{"owner_id": viewer})
	visibleTask := dbfx.Task(t, visibleAgent, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "delegated_from_task_id": hiddenTask})
	for _, task := range []string{hiddenTask, visibleTask} {
		dbfx.Insert(t, "project_graph_event", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": project, "task_id": task, "event_type": "task_finished", "node_id": task, "data": testutil.Raw(fmt.Sprintf(`'{"delegated_from_task_id":"%s"}'::jsonb`, hiddenTask))})
	}
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{UserID: parseUUID(viewer), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	r := withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/graph/events?limit=1", nil), "id", project)
	r.Header.Set("X-User-ID", viewer)
	r = r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, member))
	var response struct {
		Events []struct {
			TaskID string                     `json:"task_id"`
			Data   map[string]json.RawMessage `json:"data"`
		} `json:"events"`
	}
	testutil.Call(t, testHandler.ListProjectGraphEvents, r).Want(http.StatusOK).JSON(&response)
	if len(response.Events) != 1 || response.Events[0].TaskID != visibleTask {
		t.Fatalf("visible event must survive privacy-filtered pagination: %+v", response)
	}
	if _, present := response.Events[0].Data["delegated_from_task_id"]; present {
		t.Fatal("hidden parent leaked through metadata")
	}
}
