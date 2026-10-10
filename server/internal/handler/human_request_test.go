package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestHumanRequestVersionedResponseAndOwnership(t *testing.T) {
	issue := dbfx.Issue(t, "human decision")
	agent := dbfx.Agent(t, "human decision", testRuntimeID)
	sourceID := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": testRuntimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", sourceID)
	input := service.HumanRequestInput{Key: "publish", Kind: "confirmation", Title: "Publish to test", ActionLabel: "Publish to test", Impact: "Updates the test site", Next: "The agent will publish only to test"}
	create := func(body service.HumanRequestInput) humanRequestResponse {
		req := newRequest("POST", "/api/human-requests/", body)
		req.Header.Set("X-Task-ID", sourceID)
		req.Header.Set("X-Agent-ID", agent)
		req = legacyTaskActorRequest(t, req)
		req.Header.Set("X-Actor-Source", "task_token")
		var out humanRequestResponse
		testutil.Call(t, testHandler.CreateHumanRequest, req).Want(http.StatusCreated).JSON(&out)
		return out
	}
	first := create(input)
	duplicate := create(input)
	if first.ID != duplicate.ID || duplicate.Revision != 1 {
		t.Fatal("retried delivery created another request")
	}
	input.Title = "Publish version 2 to test"
	updated := create(input)
	if first.ID != updated.ID || updated.Revision != 2 {
		t.Fatal("changed request did not invalidate the old version")
	}
	respond := func(user string, revision int64, want int) humanRequestResponse {
		req := withURLParam(newRequest("POST", "/", service.HumanRequestAnswer{Revision: revision, Decision: "approve"}), "requestId", uuidToString(first.ID))
		req.Header.Set("X-User-ID", user)
		var out humanRequestResponse
		result := testutil.Call(t, testHandler.RespondHumanRequest, req).Want(want)
		if want == 200 {
			result.JSON(&out)
		}
		return out
	}
	respond(testUserID, 1, 409)
	other := dbfx.User(t, "other reviewer", "other-reviewer@multica.test")
	dbfx.Member(t, testWorkspaceID, other, "member")
	respond(other, 2, 403)
	accepted := respond(testUserID, 2, 200)
	replayed := respond(testUserID, 2, 200)
	if accepted.Status != "answered" || !accepted.ResponseTaskID.Valid || accepted.ResponseTaskID != replayed.ResponseTaskID {
		t.Fatal("decision did not create exactly one continuation")
	}
	task, err := testHandler.Queries.GetAgentTask(context.Background(), accepted.ResponseTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.AgentID != parseUUID(agent) || task.OriginatorUserID != parseUUID(testUserID) {
		t.Fatal("continuation changed the requesting agent or member")
	}
	var claim AgentTaskResponse
	claim.WorkspaceID = testWorkspaceID
	if err := testHandler.applyHumanResponseClaim(context.Background(), task, &claim); err != nil || claim.HumanResponsePrompt == "" {
		t.Fatalf("missing verified response context: %v", err)
	}
	retryID := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": testRuntimeID, "originator_user_id": testUserID, "accountable_user_id": testUserID, "retry_of_task_id": uuidToString(task.ID), "context": string(task.Context)})
	retry, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(retryID))
	if err != nil {
		t.Fatal(err)
	}
	if err := testHandler.applyHumanResponseClaim(context.Background(), retry, &claim); err != nil {
		t.Fatalf("a system retry lost the exact member decision: %v", err)
	}
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='cancelled' WHERE id=$1", sourceID)
	if err := testHandler.applyHumanResponseClaim(context.Background(), task, &claim); err == nil {
		t.Fatal("cancelled source still authorized a continuation")
	}
}

func TestHumanRequestReadAndArchiveAreNotConsent(t *testing.T) {
	issue := dbfx.Issue(t, "manual prerequisite")
	agent := dbfx.Agent(t, "manual prerequisite", testRuntimeID)
	id := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": testRuntimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", id)
	source, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(id))
	if err != nil {
		t.Fatal(err)
	}
	row, err := testHandler.TaskService.CreateHumanRequest(auth.WithTrustedInternalTaskActor(context.Background()), source, parseUUID(testWorkspaceID), service.HumanRequestInput{Key: "login", Kind: "manual", Title: "Sign in to the test site", Steps: []string{"Open the test site's sign-in page in the runtime browser and sign in"}, ActionLabel: "Check sign-in", Verification: "Check the authenticated session in the same browser", Next: "I will verify the session before continuing"})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, "UPDATE inbox_item SET read=true, archived=true WHERE id=$1", uuidToString(row.ID))
	got, err := testHandler.Queries.GetHumanRequest(context.Background(), db.GetHumanRequestParams{ID: row.ID, WorkspaceID: row.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" || got.ResponseTaskID.Valid {
		t.Fatal("notification actions were interpreted as consent")
	}
	dbfx.Exec(t, "UPDATE human_request SET expires_at=now()-interval '1 second' WHERE id=$1", uuidToString(row.ID))
	req := withURLParam(newRequest("POST", "/", service.HumanRequestAnswer{Revision: 1, Decision: "completed"}), "requestId", uuidToString(row.ID))
	testutil.Call(t, testHandler.RespondHumanRequest, req).Want(409)
}

func TestHumanRequestMaterialChangeRetiresTheOldCard(t *testing.T) {
	issue := dbfx.Issue(t, "original objective")
	agent := dbfx.Agent(t, "objective owner", testRuntimeID)
	id := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": testRuntimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", id)
	source, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(id))
	if err != nil {
		t.Fatal(err)
	}
	row, err := testHandler.TaskService.CreateHumanRequest(auth.WithTrustedInternalTaskActor(context.Background()), source, parseUUID(testWorkspaceID), service.HumanRequestInput{Key: "release", Kind: "confirmation", Title: "Publish original objective", ActionLabel: "Publish original objective", Next: "Only this objective will be published"})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, "UPDATE issue SET title='different objective' WHERE id=$1", issue)
	request := withURLParam(newRequest("GET", "/", nil), "requestId", uuidToString(row.ID))
	var current humanRequestResponse
	testutil.Call(t, testHandler.GetHumanRequest, request).Want(200).JSON(&current)
	if current.Status != "cancelled" || current.CanRespond {
		t.Fatal("a changed objective still exposed the old confirmation")
	}
	request = withURLParam(newRequest("POST", "/", service.HumanRequestAnswer{Revision: 1, Decision: "approve"}), "requestId", uuidToString(row.ID))
	testutil.Call(t, testHandler.RespondHumanRequest, request).Want(409)
}

func TestHumanRequestChatContinuationOwnsItsInput(t *testing.T) {
	agent := dbfx.Agent(t, "chat requester", testRuntimeID)
	chat := dbfx.ChatSession(t, agent, testutil.Cols{"creator_id": testUserID})
	id := dbfx.Task(t, agent, testutil.Cols{"chat_session_id": chat, "runtime_id": testRuntimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", id)
	source, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(id))
	if err != nil {
		t.Fatal(err)
	}
	row, err := testHandler.TaskService.CreateHumanRequest(auth.WithTrustedInternalTaskActor(context.Background()), source, parseUUID(testWorkspaceID), service.HumanRequestInput{Key: "site", Kind: "input", Title: "Provide the test site", InputLabel: "Test site address", ActionLabel: "Submit address", Next: "The agent will check this test site"})
	if err != nil {
		t.Fatal(err)
	}
	request := withURLParam(newRequest("POST", "/", service.HumanRequestAnswer{Revision: 1, Decision: "input", Answer: "https://test.example"}), "requestId", uuidToString(row.ID))
	var response humanRequestResponse
	testutil.Call(t, testHandler.RespondHumanRequest, request).Want(200).JSON(&response)
	task, err := testHandler.Queries.GetAgentTask(context.Background(), response.ResponseTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.ChatSessionID != parseUUID(chat) || task.AgentID != source.AgentID || task.ChatInputTaskID != task.ID {
		t.Fatalf("response did not own a fresh input batch: %+v", task)
	}
	messages, err := testHandler.Queries.ListChatInputMessages(context.Background(), task.ChatInputTaskID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range messages {
		if message.TaskID == task.ID && message.Role == "user" {
			found = true
		}
	}
	if !found {
		t.Fatal("queued chat continuation had no bound member response")
	}
}
