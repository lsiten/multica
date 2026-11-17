package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/mobilepush"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func mobilePushFixture(t *testing.T) (*testutil.Fixture, string, *pushRecorder, *mobilepush.Worker) {
	t.Helper()
	user := dbfx.User(t, "Mobile push", uuid.NewString()+"@push.test")
	workspace := dbfx.Workspace(t, "Mobile push", "push-"+uuid.NewString())
	fx := testutil.New(testPool, workspace, user)
	fx.Member(t, workspace, user, "member")
	device := fx.Insert(t, "mobile_push_device", testutil.Cols{
		"user_id": user, "token": strings.ReplaceAll(uuid.NewString(), "-", ""), "topic": "test.app", "environment": "sandbox", "active_since": testutil.Raw("now() - interval '1 minute'"),
	})
	fx.Cleanup(t, "DELETE FROM mobile_push_delivery WHERE device_id = $1", device)
	sender := &pushRecorder{}
	worker := mobilepush.NewWorker(testHandler.Queries, map[string]mobilepush.Sender{"sandbox": sender}, []string{"test.app"})
	return fx, device, sender, worker
}

func TestMobilePushDeliveryRechecksInboxAccessAndPreferences(t *testing.T) {
	for _, scenario := range []string{"deliver", "read", "muted", "system muted", "left workspace", "private project", "retry", "invalid device"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			fx, device, sender, worker := mobilePushFixture(t)
			project := fx.Project(t, "Restricted project", testutil.Cols{"visibility": "private", "creator_id": testUserID})
			issue := fx.Issue(t, "Private issue", testutil.Cols{"project_id": project})
			cols := testutil.Cols{"workspace_id": fx.WorkspaceID, "recipient_type": "member", "recipient_id": fx.UserID, "type": "mentioned", "severity": "info", "title": "Mention", "body": "Please review"}
			if scenario == "private project" {
				cols["issue_id"] = issue
			}
			inbox := fx.Insert(t, "inbox_item", cols)
			// Enqueue first, then change access/read state: authorization is checked at delivery time.
			if err := testHandler.Queries.EnqueueMobilePushDeliveries(ctx); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "read":
				fx.Exec(t, "UPDATE inbox_item SET read = true WHERE id = $1", inbox)
			case "muted", "system muted":
				preferences := `{"mentions":"muted"}`
				if scenario == "system muted" {
					preferences = `{"system_notifications":"muted"}`
				}
				fx.InsertNoID(t, "notification_preference", testutil.Cols{"workspace_id": fx.WorkspaceID, "user_id": fx.UserID, "preferences": preferences}, "workspace_id = $1 AND user_id = $2", fx.WorkspaceID, fx.UserID)
			case "left workspace":
				fx.Exec(t, "DELETE FROM member WHERE workspace_id = $1 AND user_id = $2", fx.WorkspaceID, fx.UserID)
			case "retry":
				sender.err = &mobilepush.ResponseError{Status: http.StatusTooManyRequests}
			case "invalid device":
				sender.err = &mobilepush.ResponseError{Status: http.StatusGone, Reason: "Unregistered"}
			}
			if err := worker.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			var attempts int
			var completed bool
			if err := testPool.QueryRow(ctx, "SELECT attempts, completed_at IS NOT NULL FROM mobile_push_delivery WHERE device_id = $1 AND inbox_id = $2", device, inbox).Scan(&attempts, &completed); err != nil {
				t.Fatal(err)
			}
			wantDelivery := scenario == "deliver" || scenario == "retry" || scenario == "invalid device"
			found := false
			for _, notification := range sender.notifications {
				if notification.InboxID == inbox {
					found = true
					if notification.RecipientID != fx.UserID {
						t.Fatal("wrong recipient")
					}
				}
			}
			if found != wantDelivery || attempts != 1 || completed != (scenario != "retry") {
				t.Fatalf("sent=%v attempts=%d completed=%v", found, attempts, completed)
			}
			if scenario == "invalid device" {
				var exists bool
				if err := testPool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM mobile_push_device WHERE id = $1)", device).Scan(&exists); err != nil {
					t.Fatal(err)
				}
				if exists {
					t.Fatal("invalid APNs device remains registered")
				}
			}
		})
	}
}

func TestMobilePushChatReplyDelivery(t *testing.T) {
	for _, scenario := range []string{"reply", "read", "chat muted", "intro", "channel", "no response", "group"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			fx, device, sender, worker := mobilePushFixture(t)
			runtime := fx.Runtime(t, "Push runtime")
			agent := fx.Agent(t, "Push agent", runtime)
			kind := "direct"
			if scenario == "group" {
				kind = "group"
			}
			session := fx.ChatSession(t, agent, testutil.Cols{"last_read_at": testutil.Raw("now() - interval '1 hour'"), "is_agent_intro": scenario == "intro", "kind": kind})
			task := fx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "chat_session_id": session, "status": "completed", "completed_at": testutil.Raw("now()")})
			fx.Exec(t, "UPDATE agent_task_queue SET chat_input_task_id = id WHERE id = $1", task)
			fx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": session, "role": "user", "content": "Review", "task_id": task, "channel_ingested": scenario == "channel"})
			messageKind := "message"
			if scenario == "no response" {
				messageKind = "no_response"
			}
			message := fx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": session, "role": "assistant", "content": "Done", "task_id": task, "message_kind": messageKind})
			if scenario == "read" {
				fx.Exec(t, "UPDATE chat_session SET last_read_at = now() WHERE id = $1", session)
			}
			if scenario == "chat muted" {
				fx.InsertNoID(t, "notification_preference", testutil.Cols{"workspace_id": fx.WorkspaceID, "user_id": fx.UserID, "preferences": `{"chat_replies":"muted"}`}, "workspace_id = $1 AND user_id = $2", fx.WorkspaceID, fx.UserID)
			}
			if err := worker.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, notification := range sender.notifications {
				if notification.MessageID == message {
					found = true
					if notification.ChatSessionID != session || notification.InboxID != "" {
						t.Fatal("chat reply lost its destination")
					}
				}
			}
			if found != (scenario == "reply") {
				t.Fatalf("chat push sent=%v", found)
			}
			if scenario == "reply" {
				if err := worker.Tick(ctx); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := testPool.QueryRow(ctx, "SELECT count(*) FROM mobile_push_delivery WHERE device_id = $1 AND chat_message_id = $2", device, message).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatalf("duplicate chat deliveries: %d", count)
				}
			}
		})
	}
}
