package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// TestDeleteWorkspace_CleansExecutionGraph verifies that deleting a workspace
// sweeps the capability-gated multiprocess-daemon tables that carry no foreign
// keys: runtime_supervisor, task_execution, execution_grant,
// execution_message_receipt, execution_snapshot, application_service_authority,
// application_service_grant and task_actor_claim. Without the explicit
// DeleteWorkspaceExecutionGraph step each of these would orphan once the
// workspace (and its runtime/task) is gone.
func TestDeleteWorkspace_CleansExecutionGraph(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	if _, err := testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, "handler-tests-delete-exec-graph"); err != nil {
		t.Fatalf("pre-clean workspace: %v", err)
	}
	var wsID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description) VALUES ($1, $2, $3) RETURNING id`,
		"Execution Graph Delete", "handler-tests-delete-exec-graph", "execution graph teardown test",
	).Scan(&wsID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID)
	})

	fx := testutil.New(testPool, wsID, testUserID)
	memberID := fx.Member(t, wsID, testUserID, "owner")
	runtimeID := fx.Runtime(t, "exec-graph-runtime")
	agentID := fx.Agent(t, "exec-graph-agent", runtimeID)
	issueID := fx.Issue(t, "exec-graph issue")
	taskID := fx.Task(t, agentID, testutil.Cols{"issue_id": issueID, "runtime_id": runtimeID})

	execID := uuid.NewString()
	seedRow(t, `INSERT INTO runtime_supervisor (runtime_id, workspace_id, daemon_id, instance_id, epoch)
		VALUES ($1, $2, 'exec-graph', gen_random_uuid(), 1)`, runtimeID, wsID)
	seedRow(t, `INSERT INTO task_execution (task_id, execution_id, runtime_id, workspace_id, daemon_id, worker_id, dispatched_at, supervisor_epoch)
		VALUES ($1, $2, $3, $4, 'exec-graph', gen_random_uuid(), now(), 1)`, taskID, execID, runtimeID, wsID)
	seedRow(t, `INSERT INTO execution_grant (token_hash, task_id, execution_id, operations, expires_at)
		VALUES ('exec-grant-hash', $1, $2, ARRAY['op1'], now() + interval '1 day')`, taskID, execID)
	seedRow(t, `INSERT INTO execution_message_receipt (execution_id, sequence, payload_hash)
		VALUES ($1, 1, 'payload-hash')`, execID)
	seedRow(t, `INSERT INTO execution_snapshot (runtime_id, snapshot_id, supervisor_epoch)
		VALUES ($1, gen_random_uuid(), 1)`, runtimeID)
	seedRow(t, `INSERT INTO application_service_authority (runtime_id, workspace_id, daemon_id, owner_id, member_id, service_instance_id, generation)
		VALUES ($1, $2, 'exec-graph', $3, $4, gen_random_uuid(), 1)`, runtimeID, wsID, testUserID, memberID)
	seedRow(t, `INSERT INTO application_service_grant (token_hash, runtime_id, generation, operations, expires_at)
		VALUES ('app-grant-hash', $1, 1, ARRAY['op1'], now() + interval '1 day')`, runtimeID)
	seedRow(t, `INSERT INTO task_actor_claim (token_hash, token_id, task_id, runtime_id, dispatched_at, agent_id, workspace_id, user_id)
		VALUES ('actor-hash', gen_random_uuid(), $1, $2, now(), $3, $4, $5)`, taskID, runtimeID, agentID, wsID, testUserID)

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/workspaces/"+wsID, nil)
	req = withURLParam(req, "id", wsID)
	testHandler.DeleteWorkspace(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteWorkspace: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	assertGone(t, "runtime_supervisor", `WHERE runtime_id = $1`, runtimeID)
	assertGone(t, "task_execution", `WHERE task_id = $1`, taskID)
	assertGone(t, "execution_grant", `WHERE task_id = $1`, taskID)
	assertGone(t, "execution_message_receipt", `WHERE execution_id = $1`, execID)
	assertGone(t, "execution_snapshot", `WHERE runtime_id = $1`, runtimeID)
	assertGone(t, "application_service_authority", `WHERE runtime_id = $1`, runtimeID)
	assertGone(t, "application_service_grant", `WHERE runtime_id = $1`, runtimeID)
	assertGone(t, "task_actor_claim", `WHERE task_id = $1`, taskID)
}

func seedRow(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed row: %v\nSQL: %s", err, sql)
	}
}

func assertGone(t *testing.T, table string, where string, arg any) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		"SELECT count(*) FROM "+table+" "+where, arg).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if n != 0 {
		t.Fatalf("%s rows survived workspace delete: %d", table, n)
	}
}
