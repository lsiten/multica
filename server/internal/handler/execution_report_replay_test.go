package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestExecutionReportReplayRecoversExpiredWorkerCredentials(t *testing.T) {
	f := newExecutionFixture(t)
	f.fx.Exec(t, "UPDATE execution_grant SET expires_at=now()-interval '1 minute' WHERE token_hash=$1", auth.HashToken(f.grant.Token))
	body := map[string]any{"identity": f.identity, "operation": "messages", "payload": map[string]any{"messages": []any{map[string]any{"id": uuid.NewString(), "seq": 1, "type": "text", "content": "persisted before outage"}}}}
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, executionRequest(http.MethodPost, f.controlPath("/executions/reports"), f.control, body)).Want(200)
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM task_message WHERE task_id=$1", f.task); count != 1 {
		t.Fatalf("recovery duplicated rows: %d", count)
	}
	body["operation"] = "usage"
	body["payload"] = map[string]any{"usage": []TaskUsagePayload{{Provider: "fixture", Model: "recovered", InputTokens: 42}}}
	testutil.Call(t, f.router.ServeHTTP, executionRequest(http.MethodPost, f.controlPath("/executions/reports"), f.control, body)).Want(200)
	if count := f.fx.Count(t, "SELECT count(*) FROM task_usage WHERE task_id=$1 AND input_tokens=42", f.task); count != 1 {
		t.Fatal("recovered usage missing")
	}
}

func TestExecutionReportReplayRejectsWrongOwnerAndStaleIdentity(t *testing.T) {
	f := newExecutionFixture(t)
	other := newExecutionFixture(t)
	body := func(identity protocol.ExecutionIdentity) map[string]any {
		return map[string]any{"identity": identity, "operation": "usage", "payload": map[string]any{"usage": []any{}}}
	}
	testutil.Call(t, other.router.ServeHTTP, executionRequest("POST", other.controlPath("/executions/reports"), other.control, body(f.identity))).Want(403)
	stale := f.identity
	stale.WorkerID = uuid.NewString()
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/executions/reports"), f.control, body(stale))).Want(409)
	stale = f.identity
	stale.DispatchedAt = stale.DispatchedAt.Add(-time.Minute)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/executions/reports"), f.control, body(stale))).Want(409)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/executions/reports"), f.grant.Token, body(f.identity))).Want(403)
}
