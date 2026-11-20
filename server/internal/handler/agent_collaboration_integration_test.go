package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestCollaborationReceiptRejectsPayloadConflict(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	workspace := parseUUID(testWorkspaceID)
	actor := parseUUID(testUserID)
	requestID := "collaboration-conflict-" + uuid.NewString()
	_, err := testHandler.Queries.CreateAgentCollaborationOperation(t.Context(), db.CreateAgentCollaborationOperationParams{
		ID: dbid.NewV7(), WorkspaceID: workspace, ActorType: "member", ActorID: actor,
		ClientRequestID: requestID, Operation: "delegate", Fingerprint: "first", Status: "accepted",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dbfx.Exec(t, `DELETE FROM agent_collaboration_operation WHERE workspace_id=$1 AND client_request_id=$2`, workspace, requestID)
	})
	_, _, err = testHandler.reserveCollaborationOperation(t.Context(), workspace, "member", actor, requestID, "delegate", "second", db.AgentTaskQueue{}, pgtype.UUID{})
	if err == nil {
		t.Fatal("same request id with a different payload was accepted")
	}
}

func TestStrictAppendCommentIsNotCompletionReconciled(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	taskID := seedBatchTask(t, "strict-append-reconcile")
	task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(taskID))
	if err != nil {
		t.Fatal(err)
	}
	comment, err := testHandler.Queries.CreateComment(t.Context(), db.CreateCommentParams{IssueID: task.IssueID, WorkspaceID: parseUUID(testWorkspaceID), AuthorType: "member", AuthorID: parseUUID(testUserID), Content: "strict append input", Type: "comment", ID: dbid.NewV7()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = testHandler.Queries.CreateAgentCollaborationOperation(t.Context(), db.CreateAgentCollaborationOperationParams{ID: dbid.NewV7(), WorkspaceID: parseUUID(testWorkspaceID), ActorType: "member", ActorID: parseUUID(testUserID), ClientRequestID: "append-reconcile-" + uuid.NewString(), Operation: "append", Fingerprint: "append", Status: "accepted", IssueID: task.IssueID, TargetAgentID: task.AgentID, TargetTaskID: task.ID, CommentID: comment.ID})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := testHandler.Queries.ListReconcilableCommentsForIssueSince(t.Context(), db.ListReconcilableCommentsForIssueSinceParams{IssueID: task.IssueID, PlannedCommentIds: []pgtype.UUID{}, CommentThreadID: pgtype.UUID{}, TaskID: task.ID, AgentID: task.AgentID, Since: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == comment.ID {
			t.Fatal("strict append comment was returned for completion reconciliation")
		}
	}
}

func TestDelegateAgentCreatesOneReceiptCommentAndRun(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	var targetRuntimeID string
	dbfx.QueryRow(t, `INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at) VALUES ($1,NULL,'collaboration-target-runtime','cloud','codex','online','target','{}'::jsonb,$2,now()) RETURNING id`, testWorkspaceID, testUserID).Scan(&targetRuntimeID)
	agentID := dbfx.Agent(t, "collaboration delegate target", targetRuntimeID, testutil.Cols{"visibility": "workspace", "permission_mode": "public_to", "instructions": "", "custom_env": testutil.Raw("'{}'::jsonb"), "custom_args": testutil.Raw("'[]'::jsonb")})
	dbfx.Exec(t, `INSERT INTO agent_invocation_target (agent_id,target_type,target_id) VALUES ($1,'workspace',$2)`, agentID, testWorkspaceID)
	issueID := dbfx.Issue(t, "collaboration delegate issue", testutil.Cols{"status": "in_progress"})
	if _, err := testHandler.Queries.GetIssueInWorkspace(t.Context(), db.GetIssueInWorkspaceParams{ID: parseUUID(issueID), WorkspaceID: parseUUID(testWorkspaceID)}); err != nil {
		t.Fatalf("seeded issue unavailable: %v", err)
	}
	requestID := "delegate-integration-" + uuid.NewString()
	req := testutil.JSONRequest(http.MethodPost, "/api/issues/"+issueID+"/collaboration/delegate", map[string]any{"target_agent_id": agentID, "content": "Review the change", "client_request_id": requestID})
	req.Header.Set("X-User-ID", testUserID)
	req = withURLParam(req, "id", issueID)
	req = req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, db.Member{Role: "owner"}))
	var response protocol.AgentCollaborationOperation
	testutil.Call(t, testHandler.DelegateAgent, req).Want(http.StatusAccepted).JSON(&response)
	if response.Status != "accepted" || response.TargetTaskID == "" || response.CommentID == "" {
		t.Fatalf("delegate receipt = %+v", response)
	}
	operation, err := testHandler.Queries.GetAgentCollaborationOperation(t.Context(), parseUUID(response.ID))
	if err != nil || operation.ClientRequestID != requestID {
		t.Fatalf("stored delegate receipt = %+v, err=%v", operation, err)
	}
	delegatedTask, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(response.TargetTaskID))
	if err != nil {
		t.Fatalf("delegated Run missing: %v", err)
	}
	if delegatedTask.RuntimeID != parseUUID(targetRuntimeID) {
		t.Fatalf("delegated Run used runtime %s, want target Agent runtime %s", uuidToString(delegatedTask.RuntimeID), targetRuntimeID)
	}
}

func TestAppendAgentBindsOnlyTheNamedRunningRun(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	var runtimeID string
	dbfx.QueryRow(t, `INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at) VALUES ($1,NULL,'collaboration-codex','cloud','codex','online','test','{}'::jsonb,$2,now()) RETURNING id`, testWorkspaceID, testUserID).Scan(&runtimeID)
	agentID := dbfx.Agent(t, "strict-append-agent", runtimeID, testutil.Cols{"visibility": "workspace", "permission_mode": "public_to", "instructions": "", "custom_env": testutil.Raw("'{}'::jsonb"), "custom_args": testutil.Raw("'[]'::jsonb")})
	dbfx.Exec(t, `INSERT INTO agent_invocation_target (agent_id,target_type,target_id) VALUES ($1,'workspace',$2)`, agentID, testWorkspaceID)
	issueID := dbfx.Issue(t, "strict-append-issue", testutil.Cols{"status": "in_progress"})
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "status": "running", "started_at": testutil.Raw("now()")})
	task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(taskID))
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `INSERT INTO task_supplement_capability (task_id, workspace_id, issue_id, capability) VALUES ($1,$2,$3,'task-supplement-v1')`, task.ID, parseUUID(testWorkspaceID), task.IssueID)
	requestID := "append-integration-" + uuid.NewString()
	req := testutil.JSONRequest(http.MethodPost, "/api/tasks/"+taskID+"/collaboration/append", map[string]any{"content": "strict input", "client_request_id": requestID})
	req.Header.Set("X-User-ID", testUserID)
	req = withURLParam(req, "taskId", taskID)
	req = req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, db.Member{Role: "owner"}))
	var response protocol.AgentCollaborationOperation
	testutil.Call(t, testHandler.AppendAgent, req).Want(http.StatusAccepted).JSON(&response)
	if response.Status != "accepted" || response.TargetTaskID != taskID || response.CommentID == "" {
		t.Fatalf("append receipt = %+v", response)
	}
	var boundTask string
	dbfx.QueryRow(t, `SELECT task_id FROM task_supplement WHERE comment_id=$1`, response.CommentID).Scan(&boundTask)
	if boundTask != taskID {
		t.Fatalf("supplement bound to %s, want %s", boundTask, taskID)
	}
}
