package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// TestDaemonRPCHandler_UserCredentialClaimsAcrossWorkspaces pins the WS-first
// claim binding (MUL-4257): a PAT-authenticated tasks.claim RPC, driven with
// the real WebSocket identity builder, reuses the HTTP claim handler and
// claims queued tasks across the owner's workspaces.
func TestDaemonRPCHandler_UserCredentialClaimsAcrossWorkspaces(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "ws-user-" + uuid.NewString()
	otherWorkspace := dbfx.Workspace(t, "WS user's second workspace", "ws-user-"+uuid.NewString())
	dbfx.Member(t, otherWorkspace, testUserID, "member")
	fixtures := []*testutil.Fixture{dbfx, testutil.New(testPool, otherWorkspace, testUserID)}
	runtimeIDs := make([]string, len(fixtures))
	for index, fixture := range fixtures {
		runtimeIDs[index] = fixture.Runtime(t, "WS user runtime", testutil.Cols{"daemon_id": daemonID})
	}
	w := httptest.NewRecorder()
	identity, ok := testHandler.buildDaemonWebSocketIdentity(w,
		newOwnerDaemonRequest(http.MethodGet, "/api/daemon/ws", nil), runtimeIDs, testUserID)
	if !ok {
		t.Fatalf("user websocket handshake failed: %d %s", w.Code, w.Body.String())
	}
	for index, fixture := range fixtures {
		for _, transport := range []string{"http", "websocket"} {
			t.Run(fixture.WorkspaceID+"/"+transport, func(t *testing.T) {
				runtimeID := runtimeIDs[index]
				agentID := fixture.Agent(t, "WS claim agent", runtimeID)
				issueID := fixture.Issue(t, "WS claim issue")
				taskID := fixture.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
				body := map[string]any{"daemon_id": daemonID, "runtime_ids": runtimeIDs, "max_tasks": 5, "force_claim": true}
				var response batchClaimReceiptResponse
				if transport == "http" {
					testutil.Call(t, testHandler.ClaimTasksByRuntime,
						newOwnerDaemonRequest(http.MethodPost, "/api/daemon/tasks/claim", body)).Want(http.StatusOK).JSON(&response)
				} else {
					requestBody, err := json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
					status, responseBody, err := testHandler.DaemonRPCHandler(ctx, identity, "tasks.claim", requestBody)
					if err != nil || status != http.StatusOK {
						t.Fatalf("RPC claim: status=%d err=%v body=%s", status, err, responseBody)
					}
					if err := json.Unmarshal(responseBody, &response); err != nil {
						t.Fatal(err)
					}
				}
				if len(response.Tasks) != 1 || response.Tasks[0].ID != taskID || response.Tasks[0].RuntimeID != runtimeID {
					t.Fatalf("claimed %+v, want queued task %s on runtime %s", response.Tasks, taskID, runtimeID)
				}
				if got := taskStatusOf(t, taskID); got != "dispatched" {
					t.Fatalf("task status = %q, want dispatched", got)
				}
			})
		}
	}
}

func TestDaemonRPCHandler_UnboundDaemonTokenCannotUseUserIdentity(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := "ws-unbound-" + uuid.NewString()
	runtimeID := dbfx.Runtime(t, "WS unbound token runtime", testutil.Cols{"daemon_id": daemonID})
	agentID := dbfx.Agent(t, "WS unbound token agent", runtimeID)
	issueID := dbfx.Issue(t, "WS unbound token issue")
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	identity := daemonws.ClientIdentity{
		DaemonID: daemonID, DaemonTokenHash: "unbound-token-hash", UserID: testUserID,
		WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID},
	}
	body, err := json.Marshal(map[string]any{"daemon_id": daemonID, "runtime_ids": []string{runtimeID}, "max_tasks": 1, "force_claim": true})
	if err != nil {
		t.Fatal(err)
	}
	status, response, err := testHandler.DaemonRPCHandler(context.Background(), identity, "tasks.claim", body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("RPC claim: status=%d err=%v body=%s", status, err, response)
	}
	if got := taskStatusOf(t, taskID); got != "queued" {
		t.Fatalf("unbound daemon credential claimed owner's task: status=%q", got)
	}
}

// TestDaemonRPCHandler_UnknownMethod returns 404 for an unknown method.
func TestDaemonRPCHandler_UnknownMethod(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	status, _, err := testHandler.DaemonRPCHandler(context.Background(),
		daemonws.ClientIdentity{DaemonID: "ws-daemon"}, "does.not.exist", nil)
	if status != http.StatusNotFound || err == nil {
		t.Fatalf("status=%d err=%v, want 404 + error", status, err)
	}
}
