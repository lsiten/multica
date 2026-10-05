package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestProgressContinueKeepsSessionAndDeduplicatesTheObservedAction(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database unavailable")
	}
	agent := dbfx.Agent(t, "continue owner", testRuntimeID)
	issue := dbfx.Issue(t, "successful run needs continuation", testutil.Cols{"status": "in_progress", "assignee_type": "agent", "assignee_id": agent})
	run := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": testRuntimeID, "status": "completed", "completed_at": testutil.Raw("now()"), "result": `{"summary":"First part delivered; remaining acceptance outstanding"}`, "originator_user_id": testUserID, "accountable_user_id": testUserID})
	current, err := testHandler.Queries.GetIssueInWorkspace(t.Context(), db.GetIssueInWorkspaceParams{ID: parseUUID(issue), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	input := service.ProgressActionInput{Kind: "inspect_continue", IssueRevision: current.Revision, RunID: run, Key: "one-follow-up"}
	post := func() string {
		var response struct {
			TaskID string `json:"task_id"`
		}
		testutil.Call(t, testHandler.PerformIssueProgressAction, withURLParam(newRequest(http.MethodPost, "/", input), "id", issue)).Want(202).JSON(&response)
		return response.TaskID
	}
	first := post()
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE issue_id=$1", issue)
	if post() != first {
		t.Fatal("same action enqueued duplicate work")
	}
	task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(first))
	if err != nil {
		t.Fatal(err)
	}
	if task.ForceFreshSession || task.RerunOfTaskID.Valid || !task.TriggerCommentID.Valid {
		t.Fatalf("continuation reset session or missed the member instruction: %+v", task)
	}
	if count := dbfx.Count(t, "SELECT count(*) FROM agent_task_queue WHERE issue_id=$1", issue); count != 2 {
		t.Fatalf("runs=%d, want 2", count)
	}
	input.Text = "different work"
	testutil.Call(t, testHandler.PerformIssueProgressAction, withURLParam(newRequest(http.MethodPost, "/", input), "id", issue)).Want(409)
	input.Text = ""
	input.Key = "second-follow-up"
	testutil.Call(t, testHandler.PerformIssueProgressAction, withURLParam(newRequest(http.MethodPost, "/", input), "id", issue)).Want(409)
}

func TestProgressRerunIsFreshAndRejectsStaleEvidence(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database unavailable")
	}
	agent := dbfx.Agent(t, "fresh rerun owner", testRuntimeID)
	issue := dbfx.Issue(t, "failed run", testutil.Cols{"status": "in_progress", "assignee_type": "agent", "assignee_id": agent})
	run := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": testRuntimeID, "status": "failed", "completed_at": testutil.Raw("now()"), "originator_user_id": testUserID, "accountable_user_id": testUserID})
	current, err := testHandler.Queries.GetIssueInWorkspace(t.Context(), db.GetIssueInWorkspaceParams{ID: parseUUID(issue), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	input := service.ProgressActionInput{Kind: "rerun", IssueRevision: current.Revision + 1, RunID: run, Key: "fresh"}
	testutil.Call(t, testHandler.PerformIssueProgressAction, withURLParam(newRequest(http.MethodPost, "/", input), "id", issue)).Want(409)
	input.IssueRevision = current.Revision
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE issue_id=$1", issue)
	var response struct {
		TaskID string `json:"task_id"`
	}
	testutil.Call(t, testHandler.PerformIssueProgressAction, withURLParam(newRequest(http.MethodPost, "/", input), "id", issue)).Want(202).JSON(&response)
	task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(response.TaskID))
	if err != nil {
		t.Fatal(err)
	}
	if !task.ForceFreshSession || uuidToString(task.RerunOfTaskID) != run {
		t.Fatalf("rerun did not bind a fresh session to the failed run: %+v", task)
	}
}

func TestProgressHandoffReportsMissingFieldsAndReviewAcceptance(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database unavailable")
	}
	agent := dbfx.Agent(t, "handoff owner", testRuntimeID)
	issue := dbfx.Issue(t, "reviewable delivery", testutil.Cols{"status": "in_review", "assignee_type": "agent", "assignee_id": agent})
	run := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": testRuntimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	current, err := testHandler.Queries.GetIssueInWorkspace(t.Context(), db.GetIssueInWorkspaceParams{ID: parseUUID(issue), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	step := service.IssueNextStep{Kind: "review", Summary: "Inspect the linked code and focused test results", ActorType: "member", ActorID: testUserID, IssueRevision: current.Revision, Evidence: []string{"focused tests passed"}}
	req := withURLParam(newRequest(http.MethodPut, "/", step), "id", issue)
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Task-ID", run)
	req.Header.Set("X-Agent-ID", agent)
	testutil.Call(t, testHandler.ReportIssueNextStep, req).Want(200)
	_, err = testHandler.Queries.CreateComment(t.Context(), db.CreateCommentParams{WorkspaceID: parseUUID(testWorkspaceID), IssueID: parseUUID(issue), AuthorType: "agent", AuthorID: parseUUID(agent), Content: "Final delivery with validation evidence", Type: "comment"})
	if err != nil {
		t.Fatal(err)
	}
	current, err = testHandler.Queries.GetIssueInWorkspace(t.Context(), db.GetIssueInWorkspaceParams{ID: parseUUID(issue), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}

	dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now(),result=$2::jsonb WHERE id=$1", run, `{"summary":"Code and validation evidence delivered"}`)
	var view service.IssueProgressView
	testutil.Call(t, testHandler.GetIssueProgress, withURLParam(newRequest(http.MethodGet, "/", nil), "id", issue)).Want(200).JSON(&view)
	if view.Root == nil || !view.Root.NeedsMe || view.Root.NextStep == nil {
		t.Fatalf("handoff missing from card: %+v", view.Root)
	}
	input := service.ProgressActionInput{Kind: "review", Verdict: "accept", IssueRevision: current.Revision, RunID: run, Key: "accept"}
	testutil.Call(t, testHandler.PerformIssueProgressAction, withURLParam(newRequest(http.MethodPost, "/", input), "id", issue)).Want(200)
	accepted, err := testHandler.Queries.GetIssueInWorkspace(t.Context(), db.GetIssueInWorkspaceParams{ID: parseUUID(issue), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != "done" {
		t.Fatalf("acceptance did not close delivery: %s", accepted.Status)
	}
	if count := dbfx.Count(t, "SELECT count(*) FROM agent_task_queue WHERE issue_id=$1", issue); count != 1 {
		t.Fatal("review acceptance started another execution")
	}

}

func TestProgressActionsRecheckRuntimeAndPendingDecision(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database unavailable")
	}
	runtime := dbfx.Runtime(t, "guarded action runtime")
	agent := dbfx.Agent(t, "guarded action agent", runtime)
	issue := dbfx.Issue(t, "guarded continuation", testutil.Cols{"status": "in_progress", "assignee_type": "agent", "assignee_id": agent})
	run := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", run)
	request := newRequest(http.MethodPost, "/", service.HumanRequestInput{Key: "guard", Kind: "confirmation", Title: "Authorize the next operation", ActionLabel: "Authorize", Next: "Continue after authorization"})
	request.Header.Set("X-Actor-Source", "task_token")
	request.Header.Set("X-Task-ID", run)
	request.Header.Set("X-Agent-ID", agent)
	testutil.Call(t, testHandler.CreateHumanRequest, request).Want(201)
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", run)
	current, err := testHandler.Queries.GetIssueInWorkspace(t.Context(), db.GetIssueInWorkspaceParams{ID: parseUUID(issue), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	input := service.ProgressActionInput{Kind: "inspect_continue", IssueRevision: current.Revision, RunID: run, Key: "guarded"}
	post := func() {
		testutil.Call(t, testHandler.PerformIssueProgressAction, withURLParam(newRequest(http.MethodPost, "/", input), "id", issue)).Want(409)
	}
	post()
	dbfx.Exec(t, "UPDATE human_request SET status='expired' WHERE source_task_id=$1", run)
	dbfx.Exec(t, "UPDATE agent_runtime SET status='offline' WHERE id=$1", runtime)
	post()
	if dbfx.Count(t, "SELECT count(*) FROM agent_task_queue WHERE issue_id=$1", issue) != 1 {
		t.Fatal("guarded operation created work")
	}
}

func TestProgressInformationReturnsToTheReporterOnAMemberAssignedIssue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database unavailable")
	}
	agent := dbfx.Agent(t, "information reporter", testRuntimeID)
	issue := dbfx.Issue(t, "human-owned work", testutil.Cols{"status": "in_progress", "assignee_type": "member", "assignee_id": testUserID})
	run := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": testRuntimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	current, err := testHandler.Queries.GetIssueInWorkspace(t.Context(), db.GetIssueInWorkspaceParams{ID: parseUUID(issue), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	step := service.IssueNextStep{Kind: "needs_information", Summary: "Provide the test URL", ActorType: "member", ActorID: testUserID, Missing: []string{"Test URL"}, IssueRevision: current.Revision}
	req := withURLParam(newRequest(http.MethodPut, "/", step), "id", issue)
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Task-ID", run)
	req.Header.Set("X-Agent-ID", agent)
	testutil.Call(t, testHandler.ReportIssueNextStep, req).Want(200)
	_, err = testHandler.Queries.CreateComment(t.Context(), db.CreateCommentParams{WorkspaceID: parseUUID(testWorkspaceID), IssueID: parseUUID(issue), AuthorType: "agent", AuthorID: parseUUID(agent), Content: "Waiting for the test URL", Type: "comment"})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", run)
	var view service.IssueProgressView
	testutil.Call(t, testHandler.GetIssueProgress, withURLParam(newRequest(http.MethodGet, "/", nil), "id", issue)).Want(200).JSON(&view)
	if view.Root == nil || view.Root.NextStep == nil || len(view.Root.Actions) != 1 || !view.Root.Actions[0].Enabled {
		t.Fatalf("final comment lost an actionable handoff: %+v", view.Root)
	}
	input := service.ProgressActionInput{Kind: "provide_info", IssueRevision: view.Root.Revision, RunID: run, Key: "reporter-answer", Text: "Test URL: https://test.example"}
	var response struct {
		TaskID string `json:"task_id"`
	}
	testutil.Call(t, testHandler.PerformIssueProgressAction, withURLParam(newRequest(http.MethodPost, "/", input), "id", issue)).Want(202).JSON(&response)
	task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(response.TaskID))
	if err != nil {
		t.Fatal(err)
	}
	if task.AgentID != parseUUID(agent) || task.ForceFreshSession {
		t.Fatalf("information did not return to its reporter: %+v", task)
	}
}
