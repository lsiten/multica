package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestHumanRequestQuickCreateCaptureTransfer(t *testing.T) {
	ctx := context.Background()
	agent := dbfx.Agent(t, "review capture", testRuntimeID)
	capture := uuid.NewString()
	issue := dbfx.Issue(t, "capture origin")
	raw, err := json.Marshal(service.QuickCreateContext{Type: service.QuickCreateContextType, WorkspaceID: testWorkspaceID, RequesterID: testUserID, Prompt: "create from captured comment", SourceContextID: capture})
	if err != nil {
		t.Fatal(err)
	}
	id := dbfx.Task(t, agent, testutil.Cols{"runtime_id": testRuntimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID, "context": string(raw)})
	dbfx.Insert(t, "issue_source_context", testutil.Cols{"id": capture, "workspace_id": testWorkspaceID, "origin_task_id": id, "source_issue_id": issue, "anchor_comment_id": uuid.NewString(), "captured_by_user_id": testUserID, "snapshot_version": 1, "snapshot": "{}", "capture_digest": "review", "state": "pending"})
	dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", id)
	source, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(id))
	if err != nil {
		t.Fatal(err)
	}
	row, err := testHandler.TaskService.CreateHumanRequest(ctx, source, parseUUID(testWorkspaceID), service.HumanRequestInput{Key: "choice", Kind: "confirmation", Title: "Create captured task", ActionLabel: "Create task", Next: "Continue the captured task"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := testHandler.TaskService.RespondHumanRequest(ctx, row, parseUUID(testUserID), service.HumanRequestAnswer{Revision: 1, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE id=$1", uuidToString(response.ResponseTaskID))
	captured, err := testHandler.Queries.GetIssueSourceContextByID(ctx, db.GetIssueSourceContextByIDParams{ID: parseUUID(capture), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	if captured.OriginTaskID != response.ResponseTaskID {
		t.Fatalf("capture still belongs to source %s, response task is %s; claim requires response ownership", uuidToString(captured.OriginTaskID), uuidToString(response.ResponseTaskID))
	}
	task, err := testHandler.Queries.GetAgentTask(ctx, response.ResponseTaskID)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(testRuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	claim, _, _, _, _, failure := testHandler.buildClaimedTaskResponse(newRequest("POST", "/", nil), &task, runtime, testRuntimeID, testWorkspaceID)
	if failure != nil || len(claim.QuickCreateSourceContext) == 0 || claim.HumanResponsePrompt == "" {
		t.Fatalf("captured continuation could not claim its source and decision: failure=%+v claim=%+v", failure, claim)
	}
}

func TestHumanRequestRevisedRequestInbox(t *testing.T) {
	ctx := context.Background()
	agent := dbfx.Agent(t, "review inbox", testRuntimeID)
	issue := dbfx.Issue(t, "review inbox")
	id := dbfx.Task(t, agent, testutil.Cols{"runtime_id": testRuntimeID, "issue_id": issue, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", id)
	source, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(id))
	if err != nil {
		t.Fatal(err)
	}
	input := service.HumanRequestInput{Key: "publish", Kind: "confirmation", Title: "Publish to test", ActionLabel: "Publish", Next: "Updates test"}
	tasks := service.NewTaskService(testHandler.Queries, testHandler.TaskService.TxStarter, nil, events.New())
	var notifications []events.Event
	tasks.Bus.Subscribe(protocol.EventInboxNew, func(event events.Event) { notifications = append(notifications, event) })
	row, err := tasks.CreateHumanRequest(ctx, source, parseUUID(testWorkspaceID), input)
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, "UPDATE inbox_item SET read=true,archived=true WHERE id=$1", row.ID)
	bot := dbfx.Insert(t, "notification_bot", testutil.Cols{"workspace_id": testWorkspaceID, "user_id": testUserID, "name": "request delivery", "platform": "wecom", "credentials": []byte("test-only"), "is_enabled": true})
	oldDelivery := dbfx.Insert(t, "notification_bot_delivery", testutil.Cols{"bot_id": bot, "inbox_id": uuidToString(row.ID), "attempts": 1})
	dbfx.Cleanup(t, "DELETE FROM notification_bot_delivery WHERE bot_id=$1", bot)
	input.Title = "Publish to production"
	input.Next = "Updates production"
	_, err = tasks.CreateHumanRequest(ctx, source, parseUUID(testWorkspaceID), input)
	if err != nil {
		t.Fatal(err)
	}
	var title, body string
	var read, archived bool
	if err := testPool.QueryRow(ctx, "SELECT title,body,read,archived FROM inbox_item WHERE id=$1", row.ID).Scan(&title, &body, &read, &archived); err != nil {
		t.Fatal(err)
	}
	if title != input.Title || body != input.Next || read || archived || len(notifications) != 2 {
		t.Fatalf("revision did not refresh the member's notification: title=%q body=%q read=%v archived=%v events=%d", title, body, read, archived, len(notifications))
	}
	var newDelivery string
	var attempts int
	dbfx.QueryRow(t, "SELECT id,attempts FROM notification_bot_delivery WHERE bot_id=$1 AND inbox_id=$2", bot, row.ID).Scan(&newDelivery, &attempts)
	if newDelivery == oldDelivery || attempts != 0 {
		t.Fatalf("revision did not replace the old delivery lease: id=%s attempts=%d", newDelivery, attempts)
	}
	if err := testHandler.Queries.FinishNotificationBotDelivery(ctx, db.FinishNotificationBotDeliveryParams{ID: parseUUID(oldDelivery), Attempts: 1, Finished: true}); err != nil {
		t.Fatal(err)
	}
	var completed bool
	dbfx.QueryRow(t, "SELECT completed_at IS NOT NULL FROM notification_bot_delivery WHERE id=$1", newDelivery).Scan(&completed)
	if completed {
		t.Fatal("an old worker finished the revised delivery")
	}
	if _, err := tasks.CreateHumanRequest(ctx, source, parseUUID(testWorkspaceID), input); err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 2 {
		t.Fatal("an unchanged delivery retry emitted another notification")
	}
}

func TestHumanRequestAutopilotChangedObjective(t *testing.T) {
	ctx := context.Background()
	agent := dbfx.Agent(t, "review autopilot", testRuntimeID)
	ap := dbfx.Insert(t, "autopilot", testutil.Cols{"workspace_id": testWorkspaceID, "title": "Deploy test", "description": "Deploy only test", "assignee_id": agent, "execution_mode": "run_only", "created_by_type": "member", "created_by_id": testUserID})
	run := dbfx.Insert(t, "autopilot_run", testutil.Cols{"autopilot_id": ap, "source": "manual", "status": "running"})
	id := dbfx.Task(t, agent, testutil.Cols{"runtime_id": testRuntimeID, "autopilot_run_id": run, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", id)
	source, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(id))
	if err != nil {
		t.Fatal(err)
	}
	row, err := testHandler.TaskService.CreateHumanRequest(ctx, source, parseUUID(testWorkspaceID), service.HumanRequestInput{Key: "publish", Kind: "confirmation", Title: "Publish test", ActionLabel: "Publish test", Next: "Publish test only"})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, "UPDATE autopilot SET title='Deploy production',description='Deploy production instead' WHERE id=$1", ap)
	response, err := testHandler.TaskService.RespondHumanRequest(ctx, row, parseUUID(testUserID), service.HumanRequestAnswer{Revision: 1, Decision: "approve"})
	if err == nil {
		dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE id=$1", uuidToString(response.ResponseTaskID))
		task, e := testHandler.Queries.GetAgentTask(ctx, response.ResponseTaskID)
		if e != nil {
			t.Fatal(e)
		}
		t.Fatalf("stale consent accepted after objective changed; new task context: %s", task.Context)
	}
	if !errors.Is(err, service.ErrHumanRequestConflict) {
		t.Fatalf("expected changed objective conflict, got %v", err)
	}
}

func TestHumanRequestAutopilotProjectContinuation(t *testing.T) {
	ctx := context.Background()
	agent := dbfx.Agent(t, "review project followup", testRuntimeID)
	project := dbfx.Project(t, "followup project")
	resource := dbfx.Insert(t, "project_resource", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": project, "resource_type": "local_directory", "resource_ref": `{"daemon_id":"test-only","local_path":"/tmp/test-only","execution_mode":"in_place"}`})
	ap := dbfx.Insert(t, "autopilot", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": project, "title": "Verify project", "description": "Verify the bound repository", "assignee_id": agent, "execution_mode": "run_only", "created_by_type": "member", "created_by_id": testUserID})
	run := dbfx.Insert(t, "autopilot_run", testutil.Cols{"autopilot_id": ap, "source": "manual", "status": "running"})
	id := dbfx.Task(t, agent, testutil.Cols{"runtime_id": testRuntimeID, "autopilot_run_id": run, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", id)
	source, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(id))
	if err != nil {
		t.Fatal(err)
	}
	row, err := testHandler.TaskService.CreateHumanRequest(ctx, source, parseUUID(testWorkspaceID), service.HumanRequestInput{Key: "verify", Kind: "confirmation", Title: "Verify project", ActionLabel: "Verify", Next: "Continue this repository"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := testHandler.TaskService.RespondHumanRequest(ctx, row, parseUUID(testUserID), service.HumanRequestAnswer{Revision: 1, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE id=$1", uuidToString(response.ResponseTaskID))
	task, err := testHandler.Queries.GetAgentTask(ctx, response.ResponseTaskID)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(testRuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	resp, _, _, _, _, failure := testHandler.buildClaimedTaskResponse(newRequest("POST", "/", nil), &task, runtime, testRuntimeID, testWorkspaceID)
	if failure != nil {
		t.Fatalf("claim failed: %+v", failure)
	}
	if resp.ProjectID != project {
		t.Fatalf("source project %s is absent in continuation claim (project_id=%q)", project, resp.ProjectID)
	}
	if len(resp.ProjectResources) != 1 || resp.ProjectResources[0].ID != resource {
		t.Fatalf("continuation lost the original project resources: %+v", resp.ProjectResources)
	}
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='dispatched',dispatched_at=now() WHERE id=$1", task.ID)
	startRequest := withURLParam(newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+uuidToString(task.ID)+"/start", nil, testWorkspaceID, "confirmed-followup"), "taskId", uuidToString(task.ID))
	testutil.Call(t, testHandler.StartTask, startRequest).Want(http.StatusOK)
	completeRequest := withURLParam(newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+uuidToString(task.ID)+"/complete", map[string]any{"output": "confirmed repository verified"}, testWorkspaceID, "confirmed-followup"), "taskId", uuidToString(task.ID))
	testutil.Call(t, testHandler.CompleteTask, completeRequest).Want(http.StatusOK)
	original, err := testHandler.Queries.GetAgentTask(ctx, source.ID)
	if err != nil || original.Status != source.Status {
		t.Fatalf("follow-up completion changed the original automation task: status=%s error=%v", original.Status, err)
	}
	// A response queued before an edit is revalidated at claim as well.
	dbfx.Exec(t, "UPDATE autopilot SET description='different objective' WHERE id=$1", ap)
	if err := testHandler.applyHumanResponseClaim(ctx, task, &resp); !errors.Is(err, service.ErrHumanRequestConflict) {
		t.Fatalf("queued response accepted changed automation: %v", err)
	}
}

func TestHumanRequestQuickCreateWaitingDoesNotFail(t *testing.T) {
	for _, state := range []string{"pending", "answered", "declined", "expired", "none"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			agent := dbfx.Agent(t, "pending creation", testRuntimeID)
			raw, err := json.Marshal(service.QuickCreateContext{Type: service.QuickCreateContextType, WorkspaceID: testWorkspaceID, RequesterID: testUserID, Prompt: "create after clarification"})
			if err != nil {
				t.Fatal(err)
			}
			id := dbfx.Task(t, agent, testutil.Cols{"runtime_id": testRuntimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID, "context": string(raw)})
			dbfx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", id)
			source, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(id))
			if err != nil {
				t.Fatal(err)
			}
			if state != "none" {
				row, err := testHandler.TaskService.CreateHumanRequest(ctx, source, parseUUID(testWorkspaceID), service.HumanRequestInput{Key: "clarification", Kind: "input", Title: "Name target site", InputLabel: "Target address", ActionLabel: "Submit target", Next: "I will create the task after this answer"})
				if err != nil {
					t.Fatal(err)
				}
				if state == "expired" {
					dbfx.Exec(t, "UPDATE human_request SET expires_at=now()-interval '1 second' WHERE id=$1", row.ID)
				}
				if state == "answered" || state == "declined" {
					answer := service.HumanRequestAnswer{Revision: 1, Decision: "input", Answer: "https://test.example"}
					if state == "declined" {
						answer.Decision, answer.Answer = "reject", ""
					}
					response, err := testHandler.TaskService.RespondHumanRequest(ctx, row, parseUUID(testUserID), answer)
					if err != nil {
						t.Fatal(err)
					}
					dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE id=$1", response.ResponseTaskID)
				}
			}
			_, err = testHandler.TaskService.CompleteTask(ctx, source.ID, []byte(`{"output":"Waiting for the requested address"}`), "", "", "", false, "", "")
			if err != nil {
				t.Fatal(err)
			}
			var count int
			dbfx.QueryRow(t, "SELECT count(*) FROM inbox_item WHERE type='quick_create_failed' AND details->>'task_id'=$1", id).Scan(&count)
			want := 0
			if state == "expired" || state == "none" {
				want = 1
			}
			if count != want {
				t.Fatalf("quick-create failure notices = %d, want %d for %s", count, want, state)
			}
		})
	}
}
