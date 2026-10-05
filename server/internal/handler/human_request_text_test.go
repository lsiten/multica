package handler

import (
	"encoding/json"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestHumanTextReplyPersistsOneDecisionReplyAndContinuation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database unavailable")
	}
	issue := dbfx.Issue(t, "text size choice")
	agent := dbfx.Agent(t, "text choice agent", testRuntimeID)
	source := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": testRuntimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", source)
	input := service.HumanRequestInput{Key: "size", Kind: "choice", Title: "Confirm dimensions", ActionLabel: "Submit dimensions", Next: "Continue with the accepted dimensions", ResponseMode: "chat_or_card", Choices: []service.HumanRequestChoice{{ID: "native", Label: "Accept 1086x1448 native size"}, {ID: "resize", Label: "Resize images"}}}
	req := newRequest(http.MethodPost, "/api/human-requests", input)
	req.Header.Set("X-Task-ID", source)
	req.Header.Set("X-Agent-ID", agent)
	req.Header.Set("X-Actor-Source", "task_token")
	var created humanRequestResponse
	testutil.Call(t, testHandler.CreateHumanRequest, req).Want(201).JSON(&created)
	beforeRuns := dbfx.Count(t, "SELECT count(*) FROM agent_task_queue WHERE issue_id=$1", issue)
	beforeComments := dbfx.Count(t, "SELECT count(*) FROM comment WHERE issue_id=$1", issue)
	var first struct {
		Request humanRequestResponse      `json:"request"`
		Reply   *service.HumanReplyOrigin `json:"reply"`
		TaskID  string                    `json:"task_id"`
	}
	answer := service.HumanTextReply{Revision: created.Revision, Text: "A", Channel: "comment", ScopeID: issue}
	post := func(reply service.HumanTextReply, member string, want int) {
		request := withURLParam(newRequest(http.MethodPost, "/", reply), "requestId", uuidToString(created.ID))
		request.Header.Set("X-User-ID", member)
		response := testutil.Call(t, testHandler.ReplyHumanRequest, request).Want(want)
		if want == 200 {
			response.JSON(&first)
		}
	}
	post(answer, testUserID, 200)
	if first.Request.Status != "answered" || first.TaskID == "" || first.Reply == nil || first.Reply.Text != "A" || first.Reply.ReplyID == "" {
		t.Fatalf("missing persisted receipt: %+v", first)
	}
	var timeline []TimelineEntry
	testutil.Call(t, testHandler.ListTimeline, withURLParam(newRequest(http.MethodGet, "/", nil), "id", issue)).Want(200).JSON(&timeline)
	found := false
	for _, entry := range timeline {
		if entry.ID == first.Reply.ReplyID {
			found = true
			if entry.HumanRequestID != nil || entry.HumanResponse == nil || entry.HumanResponse.Label != input.Choices[0].Label || entry.Content == nil || *entry.Content != "A" {
				t.Fatalf("reply is not an independent receipt: %+v", entry)
			}
		}
	}
	if !found {
		t.Fatal("formal reply missing from timeline")
	}
	firstTask := first.TaskID
	post(answer, testUserID, 200)
	if first.TaskID != firstTask || dbfx.Count(t, "SELECT count(*) FROM agent_task_queue WHERE issue_id=$1", issue) != beforeRuns+1 || dbfx.Count(t, "SELECT count(*) FROM comment WHERE issue_id=$1", issue) != beforeComments+1 {
		t.Fatal("replayed answer duplicated work")
	}
	var payload struct{ Decision, Answer string }
	if json.Unmarshal(first.Request.Response, &payload) != nil || payload.Answer != "native" {
		t.Fatalf("wrong decision: %s", first.Request.Response)
	}
	other := dbfx.User(t, "other text member", "other-text-reply@multica.test")
	dbfx.Member(t, testWorkspaceID, other, "member")
	post(answer, other, 403)
	answer.Text = "B"
	post(answer, testUserID, 409)
}

func TestHumanTextReplyCannotUseAnotherScopeOrAuthorizeCardOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database unavailable")
	}
	issue := dbfx.Issue(t, "authorization remains card only")
	agent := dbfx.Agent(t, "authorization agent", testRuntimeID)
	source := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": testRuntimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", source)
	req := newRequest(http.MethodPost, "/", service.HumanRequestInput{Key: "publish", Kind: "confirmation", Title: "Publish delivery", ActionLabel: "Publish", Next: "Publish only after approval"})
	req.Header.Set("X-Task-ID", source)
	req.Header.Set("X-Agent-ID", agent)
	req.Header.Set("X-Actor-Source", "task_token")
	var created humanRequestResponse
	testutil.Call(t, testHandler.CreateHumanRequest, req).Want(201).JSON(&created)
	post := withURLParam(newRequest(http.MethodPost, "/", service.HumanTextReply{Revision: created.Revision, Text: "yes", Channel: "comment", ScopeID: issue}), "requestId", uuidToString(created.ID))
	testutil.Call(t, testHandler.ReplyHumanRequest, post).Want(400)
	foreign := dbfx.Issue(t, "other scope")
	post = withURLParam(newRequest(http.MethodPost, "/", service.HumanTextReply{Revision: created.Revision, Text: "yes", Channel: "comment", ScopeID: foreign}), "requestId", uuidToString(created.ID))
	testutil.Call(t, testHandler.ReplyHumanRequest, post).Want(403)
}

func TestHumanTextReplyProjectsReceiptIntoChatQueueAndHistory(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database unavailable")
	}
	agent := dbfx.Agent(t, "chat text receipt", testRuntimeID)
	chat := dbfx.ChatSession(t, agent)
	source := dbfx.Task(t, agent, testutil.Cols{"chat_session_id": chat, "runtime_id": testRuntimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", source)
	payload := service.HumanRequestInput{Key: "chat-size", Kind: "choice", Title: "Choose dimensions", ActionLabel: "Submit", Next: "Continue", ResponseMode: "chat_or_card", Choices: []service.HumanRequestChoice{{ID: "native", Label: "Native dimensions"}, {ID: "resize", Label: "Resize"}}}
	create := newRequest(http.MethodPost, "/", payload)
	create.Header.Set("X-Actor-Source", "task_token")
	create.Header.Set("X-Task-ID", source)
	create.Header.Set("X-Agent-ID", agent)
	var request humanRequestResponse
	testutil.Call(t, testHandler.CreateHumanRequest, create).Want(201).JSON(&request)
	testutil.Call(t, testHandler.ReplyHumanRequest, withURLParam(newRequest(http.MethodPost, "/", service.HumanTextReply{Revision: request.Revision, Text: "A", Channel: "chat", ScopeID: chat}), "requestId", uuidToString(request.ID))).Want(200)
	var pending PendingChatTaskResponse
	testutil.Call(t, testHandler.GetPendingChatTask, withChatTestWorkspaceCtx(t, withURLParam(newRequest(http.MethodGet, "/", nil), "sessionId", chat))).Want(200).JSON(&pending)
	if len(pending.QueuedTasks) != 1 || pending.QueuedTasks[0].HumanResponse == nil || pending.QueuedTasks[0].HumanResponse.Label != "Native dimensions" {
		t.Fatalf("queue lost formal receipt: %+v", pending)
	}
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", pending.QueuedTasks[0].TaskID)
	var history []ChatMessageResponse
	testutil.Call(t, testHandler.ListChatMessages, withChatTestWorkspaceCtx(t, withURLParam(newRequest(http.MethodGet, "/", nil), "sessionId", chat))).Want(200).JSON(&history)
	found := false
	for _, message := range history {
		if message.Role == "user" {
			found = true
			if message.Content != "A" || message.HumanRequestID != nil || message.HumanResponse == nil {
				t.Fatalf("receipt confused with request card: %+v", message)
			}
		}
	}
	if !found {
		t.Fatal("persisted reply missing from history")
	}
}

func TestHumanTextAndCardRaceConsumesOnlyOneAnswer(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database unavailable")
	}
	agent := dbfx.Agent(t, "racing replies", testRuntimeID)
	issue := dbfx.Issue(t, "racing card and text")
	source := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": testRuntimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", source)
	req := newRequest(http.MethodPost, "/", service.HumanRequestInput{Key: "race", Kind: "choice", Title: "Choose dimensions", ActionLabel: "Submit", Next: "Continue", ResponseMode: "chat_or_card", Choices: []service.HumanRequestChoice{{ID: "native", Label: "Native"}, {ID: "resize", Label: "Resize"}}})
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Task-ID", source)
	req.Header.Set("X-Agent-ID", agent)
	var created humanRequestResponse
	testutil.Call(t, testHandler.CreateHumanRequest, req).Want(201).JSON(&created)
	row, err := testHandler.Queries.GetHumanRequest(t.Context(), db.GetHumanRequestParams{ID: created.ID, WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	responses := make(chan error, 2)
	go func() {
		<-start
		_, err := testHandler.TaskService.RespondHumanRequestText(t.Context(), row, parseUUID(testUserID), service.HumanTextReply{Revision: row.Revision, Text: "A", Channel: "comment", ScopeID: issue})
		responses <- err
	}()
	go func() {
		<-start
		_, err := testHandler.TaskService.RespondHumanRequest(t.Context(), row, parseUUID(testUserID), service.HumanRequestAnswer{Revision: row.Revision, Decision: "choice", Answer: "native"})
		responses <- err
	}()
	close(start)
	for range 2 {
		if err := <-responses; err != nil {
			t.Fatal(err)
		}
	}
	if dbfx.Count(t, "SELECT count(*) FROM agent_task_queue WHERE issue_id=$1", issue) != 2 || dbfx.Count(t, "SELECT count(*) FROM comment WHERE issue_id=$1", issue) != 2 {
		t.Fatal("concurrent controls duplicated the reply or continuation")
	}
	saved, err := testHandler.Queries.GetHumanRequest(t.Context(), db.GetHumanRequestParams{ID: row.ID, WorkspaceID: row.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "answered" || saved.ResponseTaskID == (pgtype.UUID{}) {
		t.Fatal("decision was not persisted")
	}
}
