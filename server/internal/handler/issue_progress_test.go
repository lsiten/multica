package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestIssueProgressHumanRequestsKeepOwnershipAndDoNotSettleStaleRequests(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database is unavailable")
	}
	f := testutil.New(testPool, testWorkspaceID, testUserID)
	issue := f.Issue(t, "progress decision", testutil.Cols{"status": "in_review"})
	agent := f.Agent(t, "progress requesting agent", testRuntimeID)
	sourceID := f.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": testRuntimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	f.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", sourceID)
	source, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(sourceID))
	if err != nil {
		t.Fatal(err)
	}
	request, err := testHandler.TaskService.CreateHumanRequest(context.Background(), source, parseUUID(testWorkspaceID), service.HumanRequestInput{Key: "accept", Kind: "confirmation", Title: "Confirm scoped delivery", ActionLabel: "Accept", Next: "Continue after the designated member decides", Details: "PRIVATE TECHNICAL BACKGROUND"})
	if err != nil {
		t.Fatal(err)
	}
	read := func(member string) service.IssueProgressView {
		req := withURLParam(newRequest(http.MethodGet, "/api/issues/"+issue+"/progress", nil), "id", issue)
		req.Header.Set("X-User-ID", member)
		var view service.IssueProgressView
		testutil.Call(t, testHandler.GetIssueProgress, req).Want(200).JSON(&view)
		return view
	}
	ownerView := read(testUserID)
	if ownerView.Root == nil || !ownerView.Root.NeedsMe || len(ownerView.Root.Requests) != 1 {
		t.Fatalf("designated member view: %+v", ownerView.Root)
	}
	other := f.User(t, "progress observer", "progress-observer@multica.test")
	f.Member(t, testWorkspaceID, other, "member")
	observerView := read(other)
	if observerView.Root == nil || observerView.Root.NeedsMe || len(observerView.Root.Requests) != 1 || observerView.Root.Requests[0].NeedsMe {
		t.Fatalf("observer response affordance leaked: %+v", observerView.Root)
	}
	f.Exec(t, "UPDATE human_request SET expires_at=now()-interval '1 second' WHERE id=$1", request.ID)
	expired := read(testUserID)
	if expired.Root == nil || len(expired.Root.Requests) != 0 {
		t.Fatalf("expired request counted: %+v", expired.Root)
	}
	f.Exec(t, "UPDATE human_request SET expires_at=now()+interval '1 day' WHERE id=$1", request.ID)
	f.Exec(t, "UPDATE issue SET description='changed objective',revision=revision+1 WHERE id=$1", issue)
	stale := read(testUserID)
	if stale.Root == nil || len(stale.Root.Requests) != 0 {
		t.Fatalf("stale request counted: %+v", stale.Root)
	}
	var status string
	f.QueryRow(t, "SELECT status FROM human_request WHERE id=$1", request.ID).Scan(&status)
	if status != "pending" {
		t.Fatalf("inspection settled request: %s", status)
	}
}

func TestIssueProgressLargeProjectKeepsCompleteCountsAcrossPages(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database is unavailable")
	}
	f := testutil.New(testPool, testWorkspaceID, testUserID)
	project := f.Project(t, "large progress")
	f.Cleanup(t, "DELETE FROM issue WHERE project_id=$1", project)
	f.Exec(t, `INSERT INTO issue(workspace_id,project_id,title,status,creator_type,creator_id,number)
	 SELECT $1,$2,'progress bulk '||n,'todo','member',$3,
	 (SELECT COALESCE(max(number),0) FROM issue WHERE workspace_id=$1)+n FROM generate_series(1,1000) n`, testWorkspaceID, project, testUserID)
	var view service.IssueProgressView
	testutil.Call(t, testHandler.GetProjectAttention, withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/attention?limit=50", nil), "id", project)).Want(200).JSON(&view)
	if !view.Complete || view.Summary.Total != 1000 || view.Summary.Open != 1000 || view.FilteredTotal != 1000 || len(view.Items) != 50 || !view.HasMore {
		t.Fatalf("large project was truncated or counted by page: %+v", view.Summary)
	}
}

func TestIssueProgressReadsDeepCrossProjectWorkWithoutWrites(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database is unavailable")
	}
	f := testutil.New(testPool, testWorkspaceID, testUserID)
	project := f.Project(t, "progress project")
	otherProject := f.Project(t, "external project")
	root := f.Issue(t, "root", testutil.Cols{"project_id": project, "status": "in_progress"})
	parent := f.Issue(t, "parent", testutil.Cols{"project_id": project, "parent_issue_id": root, "status": "in_progress"})
	leaf := f.Issue(t, "leaf", testutil.Cols{"project_id": otherProject, "parent_issue_id": parent, "status": "todo"})
	done := f.Issue(t, "done", testutil.Cols{"parent_issue_id": root, "status": "done"})
	f.Issue(t, "cancelled", testutil.Cols{"parent_issue_id": root, "status": "cancelled"})
	var runsBefore, activitiesBefore int
	f.QueryRow(t, "SELECT count(*) FROM agent_task_queue WHERE issue_id=ANY($1::uuid[])", []string{root, parent, leaf, done}).Scan(&runsBefore)
	f.QueryRow(t, "SELECT count(*) FROM activity_log WHERE issue_id=ANY($1::uuid[])", []string{root, parent, leaf, done}).Scan(&activitiesBefore)
	var view service.IssueProgressView
	testutil.Call(t, testHandler.GetIssueProgress, withURLParam(newRequest(http.MethodGet, "/api/issues/"+root+"/progress", nil), "id", root)).Want(200).JSON(&view)
	if view.Summary.Total != 4 || view.Summary.Open != 2 || view.Summary.Done != 1 || view.Summary.Closed != 1 || view.Root == nil || !view.Complete {
		t.Fatalf("deep summary: %+v", view)
	}
	if len(view.Items) != 2 {
		t.Fatalf("remaining work: %+v", view.Items)
	}
	if count := f.Count(t, "SELECT count(*) FROM agent_task_queue WHERE issue_id=ANY($1::uuid[])", []string{root, parent, leaf, done}); count != runsBefore {
		t.Fatalf("reading created runs: %d -> %d", runsBefore, count)
	}
	if count := f.Count(t, "SELECT count(*) FROM activity_log WHERE issue_id=ANY($1::uuid[])", []string{root, parent, leaf, done}); count != activitiesBefore {
		t.Fatalf("reading wrote activity: %d -> %d", activitiesBefore, count)
	}
	var projectView service.IssueProgressView
	testutil.Call(t, testHandler.GetProjectAttention, withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/attention", nil), "id", project)).Want(200).JSON(&projectView)
	if projectView.Summary.Total != 2 {
		t.Fatalf("cross-project records leaked into project count: %+v", projectView.Summary)
	}
}

func TestIssueProgressDependencyDirectionCyclesAndVersionPaging(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database is unavailable")
	}
	f := testutil.New(testPool, testWorkspaceID, testUserID)
	project := f.Project(t, "attention without supervision")
	a := f.Issue(t, "deploy", testutil.Cols{"project_id": project, "status": "todo"})
	b := f.Issue(t, "integration", testutil.Cols{"project_id": project, "status": "todo"})
	c := f.Issue(t, "fix", testutil.Cols{"project_id": project, "status": "todo"})
	f.Insert(t, "issue_dependency", testutil.Cols{"issue_id": a, "depends_on_issue_id": b, "type": "blocked_by"})
	f.Insert(t, "issue_dependency", testutil.Cols{"issue_id": c, "depends_on_issue_id": b, "type": "blocks"})
	var view service.IssueProgressView
	path := "/api/projects/" + project + "/attention?limit=1"
	testutil.Call(t, testHandler.GetProjectAttention, withURLParam(newRequest(http.MethodGet, path, nil), "id", project)).Want(200).JSON(&view)
	if view.Summary.Total != 3 || len(view.Items) != 1 || view.NextCursor == nil {
		t.Fatalf("pagination: %+v", view)
	}
	var second service.IssueProgressView
	testutil.Call(t, testHandler.GetProjectAttention, withURLParam(newRequest(http.MethodGet, path+"&cursor="+*view.NextCursor, nil), "id", project)).Want(200).JSON(&second)
	if view.Items[0].Issue.ID == second.Items[0].Issue.ID {
		t.Fatal("duplicate page")
	}
	f.Exec(t, "UPDATE issue SET status='done',revision=revision+1 WHERE id=$1", c)
	testutil.Call(t, testHandler.GetProjectAttention, withURLParam(newRequest(http.MethodGet, path+"&cursor="+*view.NextCursor, nil), "id", project)).Want(409)
	f.Exec(t, "UPDATE issue SET status='todo' WHERE id=$1", c)
	f.Insert(t, "issue_dependency", testutil.Cols{"issue_id": c, "depends_on_issue_id": a, "type": "blocked_by"})
	testutil.Call(t, testHandler.GetProjectAttention, withURLParam(newRequest(http.MethodGet, path, nil), "id", project)).Want(200).JSON(&view)
	if view.Complete {
		t.Fatalf("cycle presented as complete: %+v", view)
	}
}

func TestIssueProgressRejectsInvalidFiltersAndAgentReads(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database is unavailable")
	}
	f := testutil.New(testPool, testWorkspaceID, testUserID)
	root := f.Issue(t, "filter target")
	for _, query := range []string{"filter=invalid", "limit=0", "limit=201", "assignee_type=bot", "assignee_id=bad", "mine=maybe", "cursor=bad"} {
		t.Run(query, func(t *testing.T) {
			testutil.Call(t, testHandler.GetIssueProgress, withURLParam(newRequest(http.MethodGet, "/api/issues/"+root+"/progress?"+query, nil), "id", root)).Want(400)
		})
	}
	request := withURLParam(newRequest(http.MethodGet, "/api/issues/"+root+"/progress", nil), "id", root)
	request.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.GetIssueProgress, request).Want(403)
	foreign := f.Workspace(t, "foreign progress", "foreign-progress-test")
	request = withURLParam(newRequest(http.MethodGet, "/api/issues/"+root+"/progress", nil), "id", root)
	request.Header.Set("X-Workspace-ID", foreign)
	response := httptest.NewRecorder()
	testHandler.GetIssueProgress(response, request)
	if response.Code != http.StatusNotFound && response.Code != http.StatusForbidden {
		t.Fatalf("foreign request: %d %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["summary"]; exists {
		t.Fatal("foreign workspace summary leaked")
	}
}

func TestIssueProgressForeignDependencyMarksIncompleteWithoutDisclosingTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database is unavailable")
	}
	f := testutil.New(testPool, testWorkspaceID, testUserID)
	root := f.Issue(t, "corrupt dependency target", testutil.Cols{"status": "todo"})
	foreignWorkspace := f.Workspace(t, "private foreign workspace", "foreign-progress-dependency")
	foreign := f.Issue(t, "PRIVATE FOREIGN PREREQUISITE", testutil.Cols{"workspace_id": foreignWorkspace, "status": "todo"})
	f.Insert(t, "issue_dependency", testutil.Cols{"issue_id": root, "depends_on_issue_id": foreign, "type": "blocked_by"})
	var view service.IssueProgressView
	testutil.Call(t, testHandler.GetIssueProgress, withURLParam(newRequest(http.MethodGet, "/api/issues/"+root+"/progress", nil), "id", root)).Want(200).JSON(&view)
	if view.Complete || view.Root == nil || len(view.Root.DirectBlockers) != 0 {
		t.Fatalf("foreign dependency: %+v", view)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(foreign)) || bytes.Contains(raw, []byte("PRIVATE FOREIGN")) {
		t.Fatal("foreign target disclosed")
	}
}
