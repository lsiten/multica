package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

type executionFixture struct {
	fx                                                        *testutil.Fixture
	router                                                    http.Handler
	user, workspace, runtime, daemon, task, control, instance string
	claim                                                     time.Time
	identity                                                  protocol.ExecutionIdentity
	grant                                                     protocol.ExecutionGrantResponse
}

func executionRouter() http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.DaemonAuth(testHandler.Queries, nil, nil, nil))
	router.Post("/api/daemon/runtimes/{runtimeId}/execution-supervisor", testHandler.AcquireExecutionSupervisor)
	router.Post("/api/daemon/runtimes/{runtimeId}/tasks/{taskId}/execution", testHandler.BindTaskExecution)
	router.Post("/api/daemon/runtimes/{runtimeId}/tasks/{taskId}/execution-grants", testHandler.IssueExecutionGrant)
	router.Post("/api/daemon/runtimes/{runtimeId}/executions/reconcile", testHandler.ReconcileExecutions)
	router.Get("/api/daemon/runtimes/{runtimeId}/executions", testHandler.ListRuntimeExecutions)
	router.Post("/api/daemon/runtimes/{runtimeId}/recover-orphans", testHandler.RecoverOrphanedTasks)
	router.Post("/api/daemon/tasks/{taskId}/start", testHandler.StartTask)
	router.Post("/api/daemon/tasks/{taskId}/prepare-lease", testHandler.ExtendExecutionPrepareLease)
	router.Post("/api/daemon/tasks/{taskId}/wait-local-directory", testHandler.MarkTaskWaitingLocalDirectory)
	router.Post("/api/daemon/tasks/{taskId}/cancel-ack", testHandler.AckTaskCancelled)
	router.Post("/api/daemon/tasks/{taskId}/complete", testHandler.CompleteTask)
	router.Post("/api/daemon/tasks/{taskId}/fail", testHandler.FailTask)
	router.Post("/api/daemon/tasks/{taskId}/usage", testHandler.ReportTaskUsage)
	router.Post("/api/daemon/tasks/{taskId}/messages", testHandler.ReportTaskMessages)
	router.Post("/api/daemon/tasks/{taskId}/session", testHandler.PinTaskSession)
	router.Post("/api/daemon/tasks/{taskId}/progress", testHandler.ReportTaskProgress)
	router.Post("/api/daemon/tasks/{taskId}/jev-decision-logs", testHandler.ReportJevDecisionLog)
	router.Get("/api/daemon/tasks/{taskId}/status", testHandler.GetTaskStatus)
	return router
}

func executionRequest(method, path, token string, body any) *http.Request {
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

func (f executionFixture) controlPath(suffix string) string {
	return "/api/daemon/runtimes/" + f.runtime + suffix
}
func (f executionFixture) taskPath(operation string) string {
	return "/api/daemon/tasks/" + f.task + "/" + operation
}
func (f executionFixture) grantRequest() protocol.ExecutionGrantRequest {
	return protocol.ExecutionGrantRequest{ExecutionID: f.identity.ExecutionID, SupervisorEpoch: 1, Operations: []string{"start", "prepare-lease", "wait-local-directory", "cancel-ack", "status", "progress", "session", "complete", "fail", "usage", "messages", "jev-decision-logs"}}
}

func newExecutionFixture(t *testing.T) executionFixture {
	t.Helper()
	if testHandler == nil {
		t.Fatal("managed PostgreSQL is required")
	}
	user := dbfx.User(t, "Execution owner", "execution-"+uuid.NewString()+"@example.test")
	workspace := dbfx.Workspace(t, "Execution workspace", "execution-"+uuid.NewString())
	dbfx.Member(t, workspace, user, "owner")
	fx := testutil.New(testPool, workspace, user)
	daemonID := "execution-" + uuid.NewString()
	runtime := fx.Runtime(t, "fake execution runtime", testutil.Cols{"daemon_id": daemonID})
	agent := fx.Agent(t, "fake execution agent", runtime)
	issue := fx.Issue(t, "execution fencing", testutil.Cols{"status": "in_progress", "assignee_type": "agent", "assignee_id": agent})
	claim := time.Now().UTC().Truncate(time.Microsecond)
	task := fx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "status": "dispatched", "dispatched_at": claim, "prepare_lease_expires_at": claim.Add(time.Minute), "max_attempts": 1})
	token, err := auth.GenerateDaemonToken()
	if err != nil {
		t.Fatal(err)
	}
	fx.Insert(t, "daemon_token", testutil.Cols{"token_hash": auth.HashToken(token), "workspace_id": workspace, "daemon_id": daemonID, "expires_at": time.Now().Add(time.Hour)})
	fx.Cleanup(t, "DELETE FROM runtime_supervisor WHERE runtime_id=$1", runtime)
	fx.Cleanup(t, "DELETE FROM execution_snapshot WHERE runtime_id=$1", runtime)
	fx.Cleanup(t, "DELETE FROM task_execution WHERE task_id=$1", task)
	fx.Cleanup(t, "DELETE FROM execution_grant WHERE task_id=$1", task)
	f := executionFixture{fx: fx, router: executionRouter(), user: user, workspace: workspace, runtime: runtime, daemon: daemonID, task: task, control: token, claim: claim, instance: uuid.NewString()}
	var supervisor protocol.SupervisorResponse
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/execution-supervisor"), token, protocol.SupervisorRequest{InstanceID: f.instance})).Want(200).JSON(&supervisor)
	if supervisor.Epoch != 1 {
		t.Fatalf("initial epoch=%d", supervisor.Epoch)
	}
	bind := protocol.BindExecutionRequest{WorkerID: uuid.NewString(), DispatchedAt: claim, SupervisorEpoch: 1}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+task+"/execution"), token, bind)).Want(200).JSON(&f.identity)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+task+"/execution-grants"), token, f.grantRequest())).Want(200).JSON(&f.grant)
	fx.Cleanup(t, "DELETE FROM execution_message_receipt WHERE execution_id=$1", f.identity.ExecutionID)
	return f
}

func TestExecutionClaimAndGrantIsolation(t *testing.T) {
	f := newExecutionFixture(t)
	// A second worker may not acquire the exact same claim, including lost ACK replay.
	path := f.controlPath("/tasks/" + f.task + "/execution")
	replay := protocol.BindExecutionRequest{WorkerID: f.identity.WorkerID, DispatchedAt: f.claim, SupervisorEpoch: 1}
	var identity protocol.ExecutionIdentity
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", path, f.control, replay)).Want(200).JSON(&identity)
	if identity.ExecutionID != f.identity.ExecutionID {
		t.Fatal("replay invented an execution epoch")
	}
	replay.WorkerID = uuid.NewString()
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", path, f.control, replay)).Want(409)
	for _, tc := range []struct {
		token, path string
		body        any
		want        int
	}{
		{f.control, f.taskPath("start"), map[string]any{}, 409},
		{f.grant.Token, f.controlPath("/execution-supervisor"), protocol.SupervisorRequest{InstanceID: uuid.NewString()}, 403},
		{f.grant.Token, f.controlPath("/executions/reconcile"), map[string]any{}, 403},
		{f.grant.Token, "/api/daemon/tasks/" + uuid.NewString() + "/usage", map[string]any{}, 403},
		{"mwt_" + strings.Repeat("a", 64), f.taskPath("usage"), map[string]any{}, 401},
		{"mwt_bad", f.taskPath("usage"), map[string]any{}, 401},
	} {
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", tc.path, tc.token, tc.body)).Want(tc.want)
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM execution_grant WHERE token_hash=$1", f.grant.Token); count != 0 {
		t.Fatal("plaintext credential stored")
	}
	req := f.grantRequest()
	req.Operations = []string{"reconcile"}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution-grants"), f.control, req)).Want(400)
}

func TestExecutionCallbacksTerminalReplayAndLateReports(t *testing.T) {
	f := newExecutionFixture(t)
	start := map[string]any{"runtime_id": f.runtime, "dispatched_at": f.claim.Format(time.RFC3339Nano)}
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("start"), f.grant.Token, start)).Want(200)
	}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("session"), f.grant.Token, PinTaskSessionRequest{SessionID: "running-session"})).Want(204)
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("complete"), f.grant.Token, TaskCompleteRequest{Output: "execution done", SessionID: "running-session"})).Want(200)
	}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("fail"), f.grant.Token, TaskFailRequest{Error: "late contradiction"})).Want(409)
	usage := map[string]any{"usage": []TaskUsagePayload{{Provider: "fake", Model: "fake-model", InputTokens: 7}}}
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("usage"), f.grant.Token, usage)).Want(200)
	}
	messages := map[string]any{"messages": []map[string]any{{"seq": 1, "type": "text", "content": "late report"}}}
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("messages"), f.grant.Token, messages)).Want(200)
	}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("session"), f.grant.Token, PinTaskSessionRequest{SessionID: "late-session"})).Want(204)
	var status, session string
	f.fx.QueryRow(t, "SELECT status,COALESCE(session_id,'') FROM agent_task_queue WHERE id=$1", f.task).Scan(&status, &session)
	if status != "completed" || session != "running-session" {
		t.Fatalf("terminal state changed: %s %s", status, session)
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM task_message WHERE task_id=$1", f.task); count != 1 {
		t.Fatalf("message replay count=%d", count)
	}
	var tokens int64
	f.fx.QueryRow(t, "SELECT input_tokens FROM task_usage WHERE task_id=$1 AND provider='fake'", f.task).Scan(&tokens)
	if tokens != 7 {
		t.Fatalf("usage replay tokens=%d", tokens)
	}
	f.fx.Exec(t, "UPDATE agent_task_queue SET completed_at=now()-interval '25 hours' WHERE id=$1", f.task)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("usage"), f.grant.Token, usage)).Want(401)
}

func TestExecutionReclaimAndGrantRevocation(t *testing.T) {
	for _, scenario := range []string{"claim", "expiry", "membership", "grant"} {
		t.Run(scenario, func(t *testing.T) {
			f := newExecutionFixture(t)
			switch scenario {
			case "claim":
				f.fx.Exec(t, "UPDATE agent_task_queue SET dispatched_at=dispatched_at+interval '1 microsecond' WHERE id=$1", f.task)
			case "expiry":
				f.fx.Exec(t, "UPDATE execution_grant SET expires_at=now()-interval '1 second' WHERE task_id=$1", f.task)
			case "membership":
				f.fx.Exec(t, "DELETE FROM member WHERE workspace_id=$1 AND user_id=$2", f.workspace, f.user)
			case "grant":
				req := f.grantRequest()
				req.Operations = nil
				req.RevokeTokenHash = auth.HashToken(f.grant.Token)
				testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution-grants"), f.control, req)).Want(204)
			}
			testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("start"), f.grant.Token, map[string]any{})).Want(401)
			var status string
			f.fx.QueryRow(t, "SELECT status FROM agent_task_queue WHERE id=$1", f.task).Scan(&status)
			if status != "dispatched" {
				t.Fatalf("revoked worker wrote %s", status)
			}
		})
	}
}

func TestExecutionConcurrentSupervisorsAndSnapshotRecovery(t *testing.T) {
	f := newExecutionFixture(t)
	var group sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, executionRequest("POST", f.controlPath("/execution-supervisor"), f.control, protocol.SupervisorRequest{InstanceID: uuid.NewString(), ExpectedEpoch: 1}))
			codes <- w.Code
		}()
	}
	group.Wait()
	close(codes)
	winners := 0
	for code := range codes {
		if code == 200 {
			winners++
		} else if code != 409 {
			t.Fatalf("unexpected supervisor response %d", code)
		}
	}
	if winners != 1 {
		t.Fatalf("supervisor winners=%d", winners)
	}
	observation := protocol.ExecutionObservation{ExecutionIdentity: f.identity, State: "live"}
	snapshot := protocol.ReconcileExecutionsRequest{SnapshotID: uuid.NewString(), SupervisorEpoch: 2, Entries: []protocol.ExecutionObservation{observation}}
	path := f.controlPath("/executions/reconcile")
	var response protocol.ReconcileExecutionsResponse
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", path, f.control, snapshot)).Want(200).JSON(&response)
	if response.Complete || response.Results[0].Outcome != "pending" {
		t.Fatalf("partial snapshot mutated: %+v", response)
	}
	snapshot.Page = 1
	snapshot.Entries = nil
	snapshot.Complete = true
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", path, f.control, snapshot)).Want(200).JSON(&response)
	if !response.Complete || response.Results[0].Outcome != "adopt" {
		t.Fatalf("complete live snapshot: %+v", response)
	}
	var epoch int64
	var identity string
	f.fx.QueryRow(t, "SELECT execution_id,supervisor_epoch FROM task_execution WHERE task_id=$1", f.task).Scan(&identity, &epoch)
	if identity != f.identity.ExecutionID || epoch != 2 {
		t.Fatalf("adoption changed execution: %s %d", identity, epoch)
	}
	old := f.grantRequest()
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution-grants"), f.control, old)).Want(409)
	// Old worker grant remains valid across control takeover.
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("start"), f.grant.Token, map[string]any{})).Want(200)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/recover-orphans"), f.control, map[string]any{})).Want(200)
	var status string
	f.fx.QueryRow(t, "SELECT status FROM agent_task_queue WHERE id=$1", f.task).Scan(&status)
	if status != "running" {
		t.Fatalf("legacy recovery killed live execution: %s", status)
	}
	lost := protocol.ReconcileExecutionsRequest{SnapshotID: uuid.NewString(), SupervisorEpoch: 2, Entries: []protocol.ExecutionObservation{{ExecutionIdentity: f.identity, State: "confirmed_lost"}}, Complete: true}
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", path, f.control, lost)).Want(200).JSON(&response)
		if response.Results[0].Outcome != "recovered" {
			t.Fatalf("confirmed loss: %+v", response)
		}
	}
	f.fx.QueryRow(t, "SELECT status FROM agent_task_queue WHERE id=$1", f.task).Scan(&status)
	if status != "failed" {
		t.Fatalf("confirmed loss left %s", status)
	}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("complete"), f.grant.Token, TaskCompleteRequest{Output: "stale"})).Want(401)
}

func TestExecutionSnapshotUnknownCancelledAndMalformed(t *testing.T) {
	f := newExecutionFixture(t)
	path := f.controlPath("/executions/reconcile")
	for _, state := range []string{"unknown", "confirmed_lost"} {
		req := protocol.ReconcileExecutionsRequest{SnapshotID: uuid.NewString(), SupervisorEpoch: 1, Entries: []protocol.ExecutionObservation{{ExecutionIdentity: f.identity, State: state}}, Unknown: true}
		var response protocol.ReconcileExecutionsResponse
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", path, f.control, req)).Want(200).JSON(&response)
		if response.Results[0].Outcome != "pending" {
			t.Fatalf("unknown snapshot: %+v", response)
		}
		req.Page = 1
		req.Unknown = false
		req.Complete = true
		req.Entries = nil
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", path, f.control, req)).Want(200).JSON(&response)
		if response.Complete || response.Results[0].Outcome != "pending" {
			t.Fatal("later complete erased earlier unknown")
		}
	}
	f.fx.Exec(t, "UPDATE agent_task_queue SET status='cancelled',completed_at=now() WHERE id=$1", f.task)
	req := protocol.ReconcileExecutionsRequest{SnapshotID: uuid.NewString(), SupervisorEpoch: 1, Complete: true, Entries: []protocol.ExecutionObservation{{ExecutionIdentity: f.identity, State: "live"}}}
	var response protocol.ReconcileExecutionsResponse
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", path, f.control, req)).Want(200).JSON(&response)
	if response.Results[0].Outcome != "reject" {
		t.Fatal("adopted cancelled task")
	}
	req.Entries[0].State = "confirmed_lost"
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", path, f.control, req)).Want(409)
	req.SnapshotID = uuid.NewString()
	req.Page = 1
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", path, f.control, req)).Want(409)
	req.Page = 0
	req.Entries[0].TaskID = "malformed"
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", path, f.control, req)).Want(400)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("GET", f.controlPath("/executions?limit=101"), f.control, nil)).Want(400)
}

func TestExecutionFenceCancellationAndReclaimSerialize(t *testing.T) {
	for _, mutation := range []string{"status='cancelled'", "dispatched_at=dispatched_at+interval '1 microsecond'"} {
		t.Run(mutation, func(t *testing.T) {
			f := newExecutionFixture(t)
			tx, err := testPool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(context.Background(), "UPDATE agent_task_queue SET "+mutation+" WHERE id=$1", f.task); err != nil {
				t.Fatal(err)
			}
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				f.router.ServeHTTP(w, executionRequest("POST", f.taskPath("start"), f.grant.Token, map[string]any{}))
				result <- w
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var waiting bool
				f.fx.QueryRow(t, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '-- name: LockTaskForExecution%')").Scan(&waiting)
				if waiting {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("callback did not reach task lock")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err = tx.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case response := <-result:
				if response.Code != 409 {
					t.Fatalf("racing callback=%d %s", response.Code, response.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("callback blocked after commit")
			}
			var started bool
			f.fx.QueryRow(t, "SELECT started_at IS NOT NULL FROM agent_task_queue WHERE id=$1", f.task).Scan(&started)
			if started {
				t.Fatal("stale request started provider claim")
			}
		})
	}
}

func TestExecutionEveryReportRejectsReclaimAfterAuthentication(t *testing.T) {
	for _, operation := range []string{"complete", "fail", "usage", "messages", "session", "jev-decision-logs"} {
		t.Run(operation, func(t *testing.T) {
			f := newExecutionFixture(t)
			f.fx.Exec(t, "UPDATE agent_task_queue SET status='running',started_at=now() WHERE id=$1", f.task)
			f.fx.Cleanup(t, "DELETE FROM jev_decision_log WHERE task_id=$1", f.task)
			var body any
			switch operation {
			case "complete":
				body = TaskCompleteRequest{Output: "must not persist"}
			case "fail":
				body = TaskFailRequest{Error: "must not persist", FailureReason: "agent_error"}
			case "usage":
				body = map[string]any{"usage": []TaskUsagePayload{{Provider: "fake", Model: "must-not-persist", InputTokens: 99}}}
			case "messages":
				body = map[string]any{"messages": []map[string]any{{"seq": 1, "type": "text", "content": "must not persist"}}}
			case "session":
				body = PinTaskSessionRequest{SessionID: "must-not-persist"}
			case "jev-decision-logs":
				body = jevAuditReport(uuid.NewString(), time.Now().UTC(), "success")
			}
			tx, err := testPool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(context.Background(), "UPDATE agent_task_queue SET dispatched_at=dispatched_at+interval '1 microsecond' WHERE id=$1", f.task); err != nil {
				t.Fatal(err)
			}
			response := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				f.router.ServeHTTP(w, executionRequest("POST", f.taskPath(operation), f.grant.Token, body))
				response <- w
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var waiting bool
				f.fx.QueryRow(t, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '-- name: LockTaskForExecution%')").Scan(&waiting)
				if waiting {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("report did not wait on execution lock")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err = tx.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-response:
				if got.Code != 409 {
					t.Fatalf("stale report=%d %s", got.Code, got.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("report blocked after reclaim commit")
			}
			var status, session string
			f.fx.QueryRow(t, "SELECT status,COALESCE(session_id,'') FROM agent_task_queue WHERE id=$1", f.task).Scan(&status, &session)
			if status != "running" || session != "" {
				t.Fatalf("stale callback changed task: %s %s", status, session)
			}
			for _, table := range []string{"task_usage", "task_message", "jev_decision_log"} {
				if count := f.fx.Count(t, "SELECT count(*) FROM "+table+" WHERE task_id=$1", f.task); count != 0 {
					t.Fatalf("stale callback wrote %s", table)
				}
			}
		})
	}
}

func TestExecutionGrantRefreshExpiryAndForeignRuntime(t *testing.T) {
	f := newExecutionFixture(t)
	request := f.grantRequest()
	request.Operations = []string{"usage"}
	request.RevokeTokenHash = auth.HashToken(f.grant.Token)
	var refreshed protocol.ExecutionGrantResponse
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution-grants"), f.control, request)).Want(200).JSON(&refreshed)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("usage"), f.grant.Token, map[string]any{"usage": []any{}})).Want(401)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("start"), refreshed.Token, map[string]any{})).Want(403)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("usage"), refreshed.Token, map[string]any{"usage": []any{}})).Want(200)
	otherToken, err := auth.GenerateDaemonToken()
	if err != nil {
		t.Fatal(err)
	}
	f.fx.Insert(t, "daemon_token", testutil.Cols{"token_hash": auth.HashToken(otherToken), "workspace_id": f.workspace, "daemon_id": "another-daemon", "expires_at": time.Now().Add(time.Hour)})
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/execution-supervisor"), otherToken, protocol.SupervisorRequest{InstanceID: uuid.NewString(), ExpectedEpoch: 1})).Want(403)
	// A refreshed dispatch with an expired preparation lease cannot bind a new worker.
	f.fx.Exec(t, "UPDATE agent_task_queue SET dispatched_at=dispatched_at+interval '1 microsecond',prepare_lease_expires_at=now()-interval '1 minute' WHERE id=$1", f.task)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution"), f.control, protocol.BindExecutionRequest{WorkerID: uuid.NewString(), DispatchedAt: f.claim.Add(time.Microsecond), SupervisorEpoch: 1})).Want(409)
	var identity string
	f.fx.QueryRow(t, "SELECT execution_id FROM task_execution WHERE task_id=$1", f.task).Scan(&identity)
	if identity != f.identity.ExecutionID {
		t.Fatal("expired claim replaced execution")
	}
}

func TestExecutionInventoryPaginationAndLateDecision(t *testing.T) {
	f := newExecutionFixture(t)
	var inventory protocol.ExecutionInventoryResponse
	testutil.Call(t, f.router.ServeHTTP, executionRequest("GET", f.controlPath("/executions?limit=1"), f.control, nil)).Want(200).JSON(&inventory)
	if len(inventory.Entries) != 1 || inventory.Entries[0].ExecutionID != f.identity.ExecutionID || inventory.Entries[0].Status != "dispatched" || inventory.NextCursor != "" {
		t.Fatalf("inventory=%+v", inventory)
	}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("GET", f.controlPath("/executions?after="+f.task), f.control, nil)).Want(200).JSON(&inventory)
	if len(inventory.Entries) != 0 {
		t.Fatal("cursor duplicated task")
	}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("prepare-lease"), f.grant.Token, map[string]any{})).Want(200)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("start"), f.grant.Token, map[string]any{})).Want(200)
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("fail"), f.grant.Token, TaskFailRequest{Error: "fake failure", FailureReason: "agent_error"})).Want(200)
	}
	report := jevAuditReport(uuid.NewString(), time.Now().UTC(), "success")
	f.fx.Cleanup(t, "DELETE FROM jev_decision_log WHERE task_id=$1", f.task)
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("jev-decision-logs"), f.grant.Token, report)).Want(200)
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM jev_decision_log WHERE task_id=$1", f.task); count != 1 {
		t.Fatalf("late decision count=%d", count)
	}
}

func TestExecutionTerminalEventsObserveCommitOnce(t *testing.T) {
	for _, operation := range []string{"complete", "recover"} {
		t.Run(operation, func(t *testing.T) {
			f := newExecutionFixture(t)
			testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("start"), f.grant.Token, map[string]any{})).Want(200)
			eventType, wantStatus := protocol.EventTaskCompleted, "completed"
			if operation == "recover" {
				eventType, wantStatus = protocol.EventTaskFailed, "failed"
			}
			observed := make(chan string, 8)
			testHandler.TaskService.Bus.Subscribe(eventType, func(event events.Event) {
				if event.TaskID != f.task {
					return
				}
				var committed string
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := testPool.QueryRow(ctx, "SELECT status FROM agent_task_queue WHERE id=$1", f.task).Scan(&committed); err != nil {
					committed = "read failed"
				}
				observed <- committed
			})
			for range 2 {
				if operation == "complete" {
					testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("complete"), f.grant.Token, TaskCompleteRequest{Output: "commit proof"})).Want(200)
				} else {
					request := protocol.ReconcileExecutionsRequest{SnapshotID: uuid.NewString(), SupervisorEpoch: 1, Complete: true, Entries: []protocol.ExecutionObservation{{ExecutionIdentity: f.identity, State: "confirmed_lost"}}}
					testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/executions/reconcile"), f.control, request)).Want(200)
				}
			}
			if len(observed) != 1 {
				t.Fatalf("terminal event count=%d", len(observed))
			}
			if status := <-observed; status != wantStatus {
				t.Fatalf("event fired before %s commit: %s", wantStatus, status)
			}
		})
	}
}

func TestExecutionMessageReceiptsRejectPayloadReuseAndRollback(t *testing.T) {
	f := newExecutionFixture(t)
	original := map[string]any{"messages": []TaskMessageRequest{{Seq: 1, Type: "text", Content: "first"}}}
	for range 2 {
		var ack struct {
			Sequences []int `json:"acked_sequences"`
		}
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("messages"), f.grant.Token, original)).Want(200).JSON(&ack)
		if len(ack.Sequences) != 1 || ack.Sequences[0] != 1 {
			t.Fatalf("missing durable ACK: %+v", ack)
		}
	}
	changed := map[string]any{"messages": []TaskMessageRequest{{Seq: 2, Type: "text", Content: "rollback me"}, {Seq: 1, Type: "text", Content: "different"}}}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("messages"), f.grant.Token, changed)).Want(409)
	if count := f.fx.Count(t, "SELECT count(*) FROM task_message WHERE task_id=$1", f.task); count != 1 {
		t.Fatalf("message rollback count=%d", count)
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM execution_message_receipt WHERE execution_id=$1", f.identity.ExecutionID); count != 1 {
		t.Fatalf("receipt rollback count=%d", count)
	}
}

func TestExecutionMessageOverlappingBatchesAndNewClaimNamespace(t *testing.T) {
	f := newExecutionFixture(t)
	first := TaskMessageRequest{Seq: 1, Type: "text", Content: "first"}
	tool := TaskMessageRequest{Seq: 2, Type: "tool_use", CallID: "fake-call", Tool: "fake-tool", Input: map[string]any{"value": "fixture"}}
	result := TaskMessageRequest{Seq: 3, Type: "tool_result", CallID: "fake-call", Tool: "fake-tool", Output: "fake-result"}
	var group sync.WaitGroup
	replies := make(chan *httptest.ResponseRecorder, 2)
	for _, batch := range [][]TaskMessageRequest{{first, tool}, {tool, result}} {
		group.Add(1)
		go func(messages []TaskMessageRequest) {
			defer group.Done()
			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, executionRequest("POST", f.taskPath("messages"), f.grant.Token, map[string]any{"messages": messages}))
			replies <- w
		}(batch)
	}
	group.Wait()
	close(replies)
	for reply := range replies {
		if reply.Code != 200 {
			t.Fatalf("overlap returned %d: %s", reply.Code, reply.Body.String())
		}
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM task_message WHERE task_id=$1", f.task); count != 3 {
		t.Fatalf("overlap message count=%d", count)
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM execution_message_receipt WHERE execution_id=$1", f.identity.ExecutionID); count != 3 {
		t.Fatalf("overlap receipt count=%d", count)
	}
	// A new dispatch of the same task has a server-generated namespace; old
	// reports cannot collide with or impersonate its sequence numbers.
	f.fx.Exec(t, "UPDATE agent_task_queue SET dispatched_at=dispatched_at+interval '1 microsecond' WHERE id=$1", f.task)
	oldGrant := f.grant
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution"), f.control, protocol.BindExecutionRequest{WorkerID: uuid.NewString(), DispatchedAt: f.claim.Add(time.Microsecond), SupervisorEpoch: 1})).Want(200).JSON(&f.identity)
	f.fx.Cleanup(t, "DELETE FROM execution_message_receipt WHERE execution_id=$1", f.identity.ExecutionID)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution-grants"), f.control, f.grantRequest())).Want(200).JSON(&f.grant)
	message := map[string]any{"messages": []TaskMessageRequest{{Seq: 1, Type: "text", Content: "new claim"}}}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("messages"), oldGrant.Token, message)).Want(401)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("messages"), f.grant.Token, message)).Want(200)
	if count := f.fx.Count(t, "SELECT count(*) FROM execution_message_receipt WHERE execution_id=$1", f.identity.ExecutionID); count != 1 {
		t.Fatalf("new claim receipt count=%d", count)
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM task_message WHERE task_id=$1", f.task); count != 4 {
		t.Fatalf("new claim transcript count=%d", count)
	}
}

func TestExecutionLegacyMessagesKeepExistingProtocol(t *testing.T) {
	f := newExecutionFixture(t)
	var agentID string
	f.fx.QueryRow(t, "SELECT agent_id FROM agent_task_queue WHERE id=$1", f.task).Scan(&agentID)
	legacy := f.fx.Task(t, agentID, testutil.Cols{"runtime_id": f.runtime, "issue_id": f.fx.Issue(t, "legacy messages"), "status": "running"})
	body := map[string]any{"messages": []TaskMessageRequest{{Seq: 1, Type: "text", Content: "legacy"}}}
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", "/api/daemon/tasks/"+legacy+"/messages", f.control, body)).Want(200)
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM task_message WHERE task_id=$1", legacy); count != 2 {
		t.Fatalf("legacy message behavior changed: %d", count)
	}
}

func TestExecutionWaitCancelAckAndStaleControl(t *testing.T) {
	f := newExecutionFixture(t)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("wait-local-directory"), f.grant.Token, TaskWaitLocalDirectoryRequest{Reason: "fixture directory"})).Want(200)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("prepare-lease"), f.grant.Token, map[string]any{})).Want(200)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("start"), f.grant.Token, map[string]any{})).Want(200)
	f.fx.Exec(t, "UPDATE agent_task_queue SET status='cancelled',completed_at=now() WHERE id=$1", f.task)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("start"), f.grant.Token, map[string]any{})).Want(409)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("progress"), f.grant.Token, TaskProgressRequest{Summary: "stale progress"})).Want(409)
	ack := TaskCancelAckRequest{BranchName: "fixture-branch"}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("cancel-ack"), f.grant.Token, ack)).Want(200)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("cancel-ack"), f.control, TaskCancelAckRequest{BranchName: "legacy overwrite"})).Want(409)
	var branch, status string
	f.fx.QueryRow(t, "SELECT branch_name,status FROM agent_task_queue WHERE id=$1", f.task).Scan(&branch, &status)
	if branch != "fixture-branch" || status != "cancelled" {
		t.Fatalf("cancelled receipt state=%s %s", status, branch)
	}
}

func TestExecutionPolicyExpiryRejectsAuthorityAllowsBoundedDiagnostics(t *testing.T) {
	f := newExecutionFixture(t)
	f.fx.Exec(t, "UPDATE agent_runtime SET status='offline',last_seen_at=now()-interval '2 hours' WHERE id=$1", f.runtime)
	failed, err := testHandler.TaskService.FailTasksForOfflineRuntimes(context.Background(), db.FailTasksForOfflineRuntimesParams{ReconnectGraceSecs: 3600, MaxPerTick: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, task := range failed {
		if uuidToString(task.ID) == f.task {
			found = true
		}
	}
	if !found {
		t.Fatal("existing policy sweep did not expire execution")
	}
	testHandler.TaskService.HandleFailedTasks(context.Background(), failed)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("start"), f.grant.Token, map[string]any{})).Want(409)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("complete"), f.grant.Token, TaskCompleteRequest{Output: "must not revive"})).Want(409)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution"), f.control, protocol.BindExecutionRequest{WorkerID: f.identity.WorkerID, DispatchedAt: f.claim, SupervisorEpoch: 1})).Want(409)
	diagnostics := f.grantRequest()
	diagnostics.Operations = []string{"usage"}
	var grant protocol.ExecutionGrantResponse
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution-grants"), f.control, diagnostics)).Want(200).JSON(&grant)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("usage"), grant.Token, map[string]any{"usage": []TaskUsagePayload{{Provider: "fake", Model: "policy-diagnostics", InputTokens: 4}}})).Want(200)
	f.fx.Exec(t, "UPDATE agent_task_queue SET completed_at=now()-interval '25 hours' WHERE id=$1", f.task)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("usage"), grant.Token, map[string]any{"usage": []any{}})).Want(401)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution-grants"), f.control, diagnostics)).Want(409)
	var status string
	f.fx.QueryRow(t, "SELECT status FROM agent_task_queue WHERE id=$1", f.task).Scan(&status)
	if status != "failed" {
		t.Fatalf("policy terminal state changed: %s", status)
	}
}

func TestExecutionNormalizedCompletionReplayKeepsTransportScope(t *testing.T) {
	f := newExecutionFixture(t)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("start"), f.grant.Token, map[string]any{})).Want(200)
	request := f.grantRequest()
	request.Operations = []string{"complete"}
	var grant protocol.ExecutionGrantResponse
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution-grants"), f.control, request)).Want(200).JSON(&grant)
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("complete"), grant.Token, TaskCompleteRequest{Output: "Conversation too long. Press esc twice to go up a few messages and try again."})).Want(200)
	}
	var status, reason string
	f.fx.QueryRow(t, "SELECT status,failure_reason FROM agent_task_queue WHERE id=$1", f.task).Scan(&status, &reason)
	if status != "failed" || reason != string(taskfailure.ReasonAgentContextOverflow) {
		t.Fatalf("normalized terminal=%s %s", status, reason)
	}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("fail"), grant.Token, TaskFailRequest{Error: "not an authorized transport"})).Want(403)
}
