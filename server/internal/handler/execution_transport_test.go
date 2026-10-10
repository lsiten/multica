package handler

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type executionTransportFixture struct {
	executionFixture
	comment, project, issue string
}

func registerExecutionTransportRoutes(router chi.Router, h *Handler) {
	router.Post("/api/daemon/tasks/{taskId}/supplements/claim", h.ClaimTaskSupplement)
	router.Post("/api/daemon/tasks/{taskId}/supplements/{commentId}/ack", h.AckTaskSupplement)
	router.Post("/api/daemon/tasks/{taskId}/worktree-delivery", h.RecordWorktreeDelivery)
	router.Post("/api/daemon/tasks/{taskId}/project-graph/events", h.ReportProjectGraphEvent)
}
func newExecutionTransportFixture(t *testing.T) executionTransportFixture {
	t.Helper()
	f := newExecutionFixture(t)
	registerExecutionTransportRoutes(f.router.(*chi.Mux), testHandler)
	grant := f.grantRequest()
	grant.Operations = append(grant.Operations, "supplements/claim", "supplements/ack", "worktree-delivery", "project-graph/events")
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution-grants"), f.control, grant)).Want(200).JSON(&f.grant)
	f.fx.Exec(t, "UPDATE agent_task_queue SET status='running',started_at=now(),work_dir='/task24/work' WHERE id=$1", f.task)
	var issue string
	f.fx.QueryRow(t, "SELECT issue_id FROM agent_task_queue WHERE id=$1", f.task).Scan(&issue)
	project := f.fx.Project(t, "transport project")
	f.fx.Exec(t, "UPDATE issue SET project_id=$2 WHERE id=$1", issue, project)
	comment := f.fx.Comment(t, issue, "exactly once input")
	f.fx.Exec(t, "INSERT INTO task_supplement_capability(task_id,workspace_id,issue_id,capability) VALUES($1,$2,$3,$4)", f.task, f.workspace, issue, protocol.DaemonCapabilityTaskSupplementV1)
	f.fx.Cleanup(t, "DELETE FROM task_supplement_capability WHERE task_id=$1", f.task)
	f.fx.Exec(t, "INSERT INTO task_supplement(task_id,workspace_id,issue_id,comment_id,author_id,client_request_id,status) VALUES($1,$2,$3,$4,$5,$6,'pending')", f.task, f.workspace, issue, comment, f.user, uuid.NewString())
	f.fx.Cleanup(t, "DELETE FROM task_supplement WHERE task_id=$1", f.task)
	f.fx.Cleanup(t, "DELETE FROM project_graph_event WHERE task_id=$1", f.task)
	return executionTransportFixture{executionFixture: f, comment: comment, project: project, issue: issue}
}
func (f executionTransportFixture) body(op string) any {
	switch op {
	case "supplements/claim":
		return map[string]any{}
	case "supplements/ack":
		return map[string]any{"delivered": true}
	case "worktree-delivery":
		return map[string]any{"work_dir": "/task24/work", "branch_name": "agent/task24", "worktree_commit": strings.Repeat("a", 40)}
	default:
		return map[string]any{"project_id": f.project, "event_type": "task_finished", "node_id": f.task, "data": map[string]any{"duration_ms": 1}}
	}
}
func (f executionTransportFixture) path(op string) string {
	if op == "supplements/ack" {
		return f.taskPath("supplements/" + f.comment + "/ack")
	}
	return f.taskPath(op)
}
func (f executionTransportFixture) assertUnchanged(t *testing.T, op string) {
	t.Helper()
	switch op {
	case "supplements/claim", "supplements/ack":
		var status string
		f.fx.QueryRow(t, "SELECT status FROM task_supplement WHERE task_id=$1 AND comment_id=$2", f.task, f.comment).Scan(&status)
		want := "pending"
		if op == "supplements/ack" {
			want = "delivering"
		}
		if status != want {
			t.Fatalf("supplement changed: %s", status)
		}
	case "worktree-delivery":
		var branch string
		f.fx.QueryRow(t, "SELECT COALESCE(branch_name,'') FROM agent_task_queue WHERE id=$1", f.task).Scan(&branch)
		if branch != "" {
			t.Fatal("worktree write escaped transaction")
		}
	default:
		if n := f.fx.Count(t, "SELECT count(*) FROM project_graph_event WHERE task_id=$1", f.task); n != 0 {
			t.Fatal("graph write escaped transaction")
		}
	}
}
func TestExecutionTransportHappyPathsAndLateAcknowledgement(t *testing.T) {
	f := newExecutionTransportFixture(t)
	var claimed struct {
		CommentID string `json:"comment_id"`
	}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.path("supplements/claim"), f.grant.Token, f.body("supplements/claim"))).Want(200).JSON(&claimed)
	if claimed.CommentID != f.comment {
		t.Fatal("claimed another comment")
	}
	f.fx.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", f.task)
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.path("supplements/ack"), f.grant.Token, f.body("supplements/ack"))).Want(200)
	}
	var status string
	var attempts int
	f.fx.QueryRow(t, "SELECT status,attempt_count FROM task_supplement WHERE task_id=$1 AND comment_id=$2", f.task, f.comment).Scan(&status, &attempts)
	if status != "delivered" || attempts != 1 {
		t.Fatalf("ACK reinjected input: %s %d", status, attempts)
	}
	for range 2 {
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.path("worktree-delivery"), f.grant.Token, f.body("worktree-delivery"))).Want(200)
	}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.path("project-graph/events"), f.grant.Token, f.body("project-graph/events"))).Want(201)
	if n := f.fx.Count(t, "SELECT count(*) FROM project_graph_event WHERE task_id=$1 AND project_id=$2 AND workspace_id=$3", f.task, f.project, f.workspace); n != 1 {
		t.Fatal("graph scope mismatch")
	}
}
func TestExecutionTransportRejectsClosedPathsAndMalformedBodies(t *testing.T) {
	f := newExecutionTransportFixture(t)
	for _, path := range []string{f.taskPath("supplements/../claim"), f.taskPath("supplements/" + f.comment + "/ack/extra"), f.taskPath("%77orktree-delivery"), f.taskPath("project-graph%2fevents"), f.taskPath("//worktree-delivery"), f.path("worktree-delivery") + "?unexpected=1", f.taskPath("identity/email"), "/api/human-requests/"} {
		testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", path, f.grant.Token, map[string]any{})).Want(403)
	}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("GET", f.path("supplements/claim"), f.grant.Token, nil)).Want(403)
	for _, op := range []string{"supplements/claim", "supplements/ack", "worktree-delivery", "project-graph/events"} {
		for _, raw := range []string{`{"extra":true}`, `{} {}`, `{"unknown":"` + strings.Repeat("x", 140<<10) + `"}`} {
			request := httptest.NewRequest("POST", f.path(op), strings.NewReader(raw))
			request.Header.Set("Authorization", "Bearer "+f.grant.Token)
			testutil.Call(t, f.router.ServeHTTP, request).Want(400)
		}
	}
	limited := f.grantRequest()
	limited.Operations = []string{"status"}
	var grant protocol.ExecutionGrantResponse
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution-grants"), f.control, limited)).Want(200).JSON(&grant)
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.path("supplements/claim"), grant.Token, map[string]any{})).Want(403)
	bad := f.grantRequest()
	bad.Operations = []string{"supplements/" + f.comment + "/ack"}
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.controlPath("/tasks/"+f.task+"/execution-grants"), f.control, bad)).Want(400)
}
func TestExecutionTransportForeignEntitiesAndRollback(t *testing.T) {
	for _, op := range []string{"supplements/claim", "supplements/ack", "worktree-delivery", "project-graph/events"} {
		t.Run(op, func(t *testing.T) {
			f := newExecutionTransportFixture(t)
			if op == "supplements/ack" {
				f.fx.Exec(t, "UPDATE task_supplement SET status='delivering',attempt_count=1 WHERE task_id=$1", f.task)
			}
			copyHandler := *testHandler
			copyHandler.TxStarter = rollbackOnCommitTxStarter{pool: testPool}
			router := chi.NewRouter()
			router.Use(middleware.DaemonAuth(testHandler.Queries, nil, nil, nil))
			registerExecutionTransportRoutes(router, &copyHandler)
			testutil.Call(t, router.ServeHTTP, executionRequest("POST", f.path(op), f.grant.Token, f.body(op))).Want(500)
			f.assertUnchanged(t, op)
		})
	}
	f := newExecutionTransportFixture(t)
	foreignComment := f.fx.Comment(t, f.issue, "not bound to task")
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.taskPath("supplements/"+foreignComment+"/ack"), f.grant.Token, map[string]any{"delivered": true})).Want(409)
	otherProject := f.fx.Project(t, "unrelated project")
	body := f.body("project-graph/events").(map[string]any)
	body["project_id"] = otherProject
	testutil.Call(t, f.router.ServeHTTP, executionRequest("POST", f.path("project-graph/events"), f.grant.Token, body)).Want(409)
}
func waitExecutionTransportLock(t *testing.T) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := testPool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '-- name: LockTaskForExecution%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("callback did not reach execution lock")
		case <-ticker.C:
		}
	}
}
func TestExecutionTransportRejectsClaimChangeAndExpiryAfterLockWait(t *testing.T) {
	for _, op := range []string{"supplements/claim", "supplements/ack", "worktree-delivery", "project-graph/events"} {
		for _, mode := range []string{"reclaim", "expiry", "membership", "cancel"} {
			t.Run(op+"/"+mode, func(t *testing.T) {
				f := newExecutionTransportFixture(t)
				if op == "supplements/ack" {
					f.fx.Exec(t, "UPDATE task_supplement SET status='delivering',attempt_count=1 WHERE task_id=$1", f.task)
				}
				if mode == "expiry" {
					f.fx.Exec(t, "UPDATE execution_grant SET expires_at=now()+interval '350 milliseconds' WHERE token_hash=$1", auth.HashToken(f.grant.Token))
				}
				tx, err := testPool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(context.Background())
				if _, err = tx.Exec(t.Context(), "SELECT id FROM agent_task_queue WHERE id=$1 FOR UPDATE", f.task); err != nil {
					t.Fatal(err)
				}
				result := make(chan *httptest.ResponseRecorder, 1)
				go func() {
					recorder := httptest.NewRecorder()
					f.router.ServeHTTP(recorder, executionRequest("POST", f.path(op), f.grant.Token, f.body(op)))
					result <- recorder
				}()
				waitExecutionTransportLock(t)
				switch mode {
				case "reclaim":
					_, err = tx.Exec(t.Context(), "UPDATE agent_task_queue SET dispatched_at=dispatched_at+interval '1 microsecond' WHERE id=$1", f.task)
				case "membership":
					_, err = tx.Exec(t.Context(), "DELETE FROM member WHERE user_id=$1 AND workspace_id=$2", f.user, f.workspace)
				case "cancel":
					_, err = tx.Exec(t.Context(), "UPDATE task_execution SET revoked=true WHERE task_id=$1", f.task)
				case "expiry":
					for {
						var expired bool
						f.fx.QueryRow(t, "SELECT expires_at<clock_timestamp() FROM execution_grant WHERE token_hash=$1", auth.HashToken(f.grant.Token)).Scan(&expired)
						if expired {
							break
						}
						select {
						case <-t.Context().Done():
							t.Fatal("test canceled")
						case <-time.After(5 * time.Millisecond):
						}
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
				select {
				case response := <-result:
					if response.Code != 409 {
						t.Fatalf("late authority accepted: %d %s", response.Code, response.Body.String())
					}
				case <-time.After(5 * time.Second):
					t.Fatal("callback did not unblock")
				}
				f.assertUnchanged(t, op)
			})
		}
	}
}

type executionTransportPausedRow struct {
	row     pgx.Row
	entered chan struct{}
	release <-chan struct{}
	ctx     context.Context
}

func (r executionTransportPausedRow) Scan(values ...any) error {
	err := r.row.Scan(values...)
	if err == nil {
		close(r.entered)
		select {
		case <-r.release:
		case <-r.ctx.Done():
			return r.ctx.Err()
		}
	}
	return err
}

type executionTransportPausedTx struct {
	pgx.Tx
	target  string
	entered chan struct{}
	release <-chan struct{}
}

func (tx executionTransportPausedTx) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	row := tx.Tx.QueryRow(ctx, query, args...)
	if strings.HasPrefix(query, "-- name: "+tx.target+" ") {
		return executionTransportPausedRow{row: row, entered: tx.entered, release: tx.release, ctx: ctx}
	}
	return row
}

type executionTransportPausedStarter struct {
	target  string
	entered chan struct{}
	release <-chan struct{}
}

func (s executionTransportPausedStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := testPool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return executionTransportPausedTx{Tx: tx, target: s.target, entered: s.entered, release: s.release}, nil
}
func TestExecutionTransportExpiryBeforeCommitRollsBack(t *testing.T) {
	targets := map[string]string{"supplements/claim": "ClaimNextTaskSupplement", "supplements/ack": "AckTaskSupplementDelivered", "worktree-delivery": "RecordSharedWorktreeDelivery", "project-graph/events": "CreateProjectGraphEvent"}
	for op, target := range targets {
		t.Run(op, func(t *testing.T) {
			f := newExecutionTransportFixture(t)
			if op == "supplements/ack" {
				f.fx.Exec(t, "UPDATE task_supplement SET status='delivering',attempt_count=1 WHERE task_id=$1", f.task)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			h := *testHandler
			h.TxStarter = executionTransportPausedStarter{target: target, entered: entered, release: release}
			router := chi.NewRouter()
			router.Use(middleware.DaemonAuth(testHandler.Queries, nil, nil, nil))
			registerExecutionTransportRoutes(router, &h)
			f.fx.Exec(t, "UPDATE execution_grant SET expires_at=clock_timestamp()+interval '700 milliseconds' WHERE token_hash=$1", auth.HashToken(f.grant.Token))
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				response := httptest.NewRecorder()
				router.ServeHTTP(response, executionRequest("POST", f.path(op), f.grant.Token, f.body(op)))
				done <- response
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("write did not reach precommit pause")
			}
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				var expired bool
				f.fx.QueryRow(t, "SELECT expires_at<clock_timestamp() FROM execution_grant WHERE token_hash=$1", auth.HashToken(f.grant.Token)).Scan(&expired)
				if expired {
					break
				}
				select {
				case <-deadline.C:
					t.Fatal("grant did not expire")
				case <-ticker.C:
				}
			}
			once.Do(func() { close(release) })
			select {
			case response := <-done:
				if response.Code != 409 {
					t.Fatalf("expired write committed: %d %s", response.Code, response.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("callback did not exit")
			}
			f.assertUnchanged(t, op)
		})
	}
}

func TestExecutionTransportRechecksProjectAfterTaskLock(t *testing.T) {
	f := newExecutionTransportFixture(t)
	other := f.fx.Project(t, "replacement source project")
	tx, err := testPool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), "SELECT id FROM agent_task_queue WHERE id=$1 FOR UPDATE", f.task); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		f.router.ServeHTTP(response, executionRequest("POST", f.path("project-graph/events"), f.grant.Token, f.body("project-graph/events")))
		done <- response
	}()
	waitExecutionTransportLock(t)
	if _, err = tx.Exec(t.Context(), "UPDATE issue SET project_id=$2 WHERE id=$1", f.issue, other); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-done:
		if response.Code != 409 {
			t.Fatalf("stale project accepted: %d", response.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("graph callback blocked")
	}
	f.assertUnchanged(t, "project-graph/events")
}
