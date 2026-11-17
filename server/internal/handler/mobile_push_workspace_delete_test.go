package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestDeleteWorkspaceRemovesOnlyItsMobilePushDeliveries(t *testing.T) {
	ctx := context.Background()
	device := dbfx.Insert(t, "mobile_push_device", testutil.Cols{
		"user_id": testUserID, "token": strings.ReplaceAll(uuid.NewString(), "-", ""), "topic": "test.app", "environment": "sandbox",
	})
	type receipt struct {
		id        string
		workspace string
	}
	var receipts []receipt
	victim := dbfx.Workspace(t, "Push delete target", "push-delete-"+uuid.NewString())
	neighbor := dbfx.Workspace(t, "Push delete neighbor", "push-neighbor-"+uuid.NewString())
	for _, workspace := range []string{victim, neighbor} {
		fx := testutil.New(testPool, workspace, testUserID)
		fx.Member(t, workspace, testUserID, "owner")
		inbox := fx.Insert(t, "inbox_item", testutil.Cols{"workspace_id": workspace, "recipient_type": "member", "recipient_id": testUserID, "type": "mentioned", "severity": "info", "title": "Mention", "body": "Review"})
		runtime := fx.Runtime(t, "Push cleanup runtime")
		agent := fx.Agent(t, "Push cleanup agent", runtime)
		session := fx.ChatSession(t, agent)
		message := fx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": session, "role": "assistant", "content": "Ready"})
		for key, value := range map[string]string{"inbox_id": inbox, "chat_message_id": message} {
			id := fx.Insert(t, "mobile_push_delivery", testutil.Cols{"device_id": device, key: value})
			receipts = append(receipts, receipt{id, workspace})
		}
	}
	req := newRequest(http.MethodDelete, "/api/workspaces/"+victim, nil)
	req.Header.Set("X-Workspace-ID", victim)
	testutil.Call(t, testHandler.DeleteWorkspace, withURLParam(req, "id", victim)).Want(http.StatusNoContent)
	for _, receipt := range receipts {
		var exists bool
		if err := testPool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM mobile_push_delivery WHERE id = $1)", receipt.id).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != (receipt.workspace == neighbor) {
			t.Errorf("delivery %s in workspace %s exists=%v", receipt.id, receipt.workspace, exists)
		}
	}
	var exists bool
	if err := testPool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM mobile_push_device WHERE id = $1)", device).Scan(&exists); err != nil || !exists {
		t.Fatalf("account device registration was removed: exists=%v err=%v", exists, err)
	}
}
