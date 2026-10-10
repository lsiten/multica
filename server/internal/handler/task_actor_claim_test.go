package handler

import (
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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type taskActorFixture struct {
	fx                                                                 *testutil.Fixture
	router                                                             http.Handler
	user, workspace, runtime, agent, task, issue, project, token, chat string
	dispatch                                                           time.Time
}

func taskActorRouter(h *Handler, afterAuth func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Auth(h.Queries, nil, nil, nil))
	if afterAuth != nil {
		r.Use(afterAuth)
	}
	r.Post("/api/human-requests/", h.CreateHumanRequest)
	r.Post("/api/projects/{id}/supervision/actions", h.ApplyProjectSupervision)
	r.Post("/api/projects/{id}/supervision/report", h.ReportProjectSupervision)
	return r
}
func newTaskActorFixture(t *testing.T, project bool, legacy ...bool) taskActorFixture {
	t.Helper()
	if testHandler == nil {
		t.Fatal("owned database required")
	}
	user := dbfx.User(t, "actor fixture", "actor-"+uuid.NewString()+"@example.test")
	workspace := dbfx.Workspace(t, "actor fixture", "actor-"+uuid.NewString())
	dbfx.Member(t, workspace, user, "owner")
	fx := testutil.New(testPool, workspace, user)
	runtime := fx.Runtime(t, "fake actor runtime", testutil.Cols{"daemon_id": "actor-" + uuid.NewString(), "owner_id": user, "metadata": testutil.Raw(`'{"capabilities":["project-supervision-v1"]}'::jsonb`)})
	agent := fx.Agent(t, "actor lead", runtime, testutil.Cols{"owner_id": user})
	issue := fx.Issue(t, "actor target", testutil.Cols{"status": "todo"})
	dispatch := time.Now().UTC().Truncate(time.Microsecond)
	columns := testutil.Cols{"runtime_id": runtime, "status": "dispatched", "dispatched_at": dispatch, "originator_user_id": user, "accountable_user_id": user}
	projectID := ""
	if project {
		projectID = fx.Project(t, "actor project", testutil.Cols{"lead_type": "agent", "lead_id": agent, "status": "in_progress"})
		fx.Exec(t, "UPDATE issue SET project_id=$2 WHERE id=$1", issue, projectID)
		if err := testHandler.Queries.SeedIssueStatusEntries(t.Context(), parseUUID(workspace)); err != nil {
			t.Fatal(err)
		}
		fx.Cleanup(t, "DELETE FROM issue_status WHERE workspace_id=$1", workspace)
		p, err := testHandler.Queries.GetProjectInWorkspace(t.Context(), db.GetProjectInWorkspaceParams{ID: parseUUID(projectID), WorkspaceID: parseUUID(workspace)})
		if err != nil {
			t.Fatal(err)
		}
		if err = testHandler.ProjectSupervisionService.Save(t.Context(), p, parseUUID(user), true, service.DefaultProjectSupervisionConfig(), 0); err != nil {
			t.Fatal(err)
		}
		fx.Cleanup(t, "DELETE FROM project_supervision WHERE project_id=$1", projectID)
		raw, _ := json.Marshal(service.ProjectCoordinationContext{Type: service.ProjectSupervisionContextType, WorkspaceID: workspace, ProjectID: projectID, RequesterID: user, PolicyRevision: 1, CheckedVersion: 1})
		columns["context"] = string(raw)
	} else {
		columns["issue_id"] = issue
	}
	task := fx.Task(t, agent, columns)
	fx.Cleanup(t, "DELETE FROM task_token WHERE task_id=$1", task)
	fx.Cleanup(t, "DELETE FROM task_actor_claim WHERE task_id=$1", task)
	fx.Cleanup(t, "DELETE FROM human_request WHERE source_task_id=$1", task)
	fx.Cleanup(t, "DELETE FROM comment WHERE id IN(SELECT id FROM human_request WHERE source_task_id=$1)", task)
	fx.Cleanup(t, "DELETE FROM inbox_item WHERE id IN(SELECT id FROM human_request WHERE source_task_id=$1)", task)
	fx.Cleanup(t, "DELETE FROM agent_task_queue WHERE delegated_from_task_id=$1", task)
	f := taskActorFixture{fx: fx, router: taskActorRouter(testHandler, nil), user: user, workspace: workspace, runtime: runtime, agent: agent, task: task, issue: issue, project: projectID, dispatch: dispatch}
	if len(legacy) > 0 && legacy[0] {
		token, err := auth.GenerateAgentTaskToken()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = testHandler.Queries.CreateTaskToken(t.Context(), db.CreateTaskTokenParams{TokenHash: auth.HashToken(token), TaskID: parseUUID(task), AgentID: parseUUID(agent), WorkspaceID: parseUUID(workspace), UserID: parseUUID(user), ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}}); err != nil {
			t.Fatal(err)
		}
		f.token = token
	} else {
		f.token = f.mint(t)
	}
	fx.Exec(t, "UPDATE agent_task_queue SET status='running',started_at=now() WHERE id=$1", task)
	return f
}
func (f taskActorFixture) mint(t *testing.T) string {
	t.Helper()
	task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(f.task))
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.GenerateAgentTaskToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = testHandler.TaskService.FinalizeTaskClaim(t.Context(), task, db.CreateTaskTokenParams{TokenHash: auth.HashToken(token), TaskID: task.ID, AgentID: task.AgentID, WorkspaceID: parseUUID(f.workspace), UserID: parseUUID(f.user), ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}}, nil, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return token
}
func (f taskActorFixture) path(op string) string {
	if op == "human" {
		return "/api/human-requests/"
	}
	return "/api/projects/" + f.project + "/supervision/" + op
}
func (f taskActorFixture) body(op string) any {
	switch op {
	case "human":
		return service.HumanRequestInput{Key: "confirm", Kind: "confirmation", Title: "Approve fixture action", ActionLabel: "Approve", Next: "Perform only approved fixture action"}
	case "actions":
		return map[string]any{"checked_version": 1, "actions": []service.ProjectSupervisionAction{{IssueID: f.issue, Revision: 1, Kind: "assign", AssigneeType: "agent", AssigneeID: f.agent}}}
	default:
		return service.ProjectSupervisionReport{TaskID: f.task, CheckedVersion: 1, Decision: "wait", Summary: "Waiting for explicit fixture input"}
	}
}
func actorRequest(method, path, token string, body any) *http.Request {
	return executionRequest(method, path, token, body)
}
func (f taskActorFixture) effectCounts(t *testing.T) map[string]int {
	t.Helper()
	return map[string]int{
		"human_request":             f.fx.Count(t, "SELECT count(*) FROM human_request WHERE source_task_id=$1", f.task),
		"comment":                   f.fx.Count(t, "SELECT count(*) FROM comment WHERE workspace_id=$1", f.workspace),
		"chat_message":              f.fx.Count(t, "SELECT count(*) FROM chat_message m JOIN chat_session c ON c.id=m.chat_session_id WHERE c.workspace_id=$1", f.workspace),
		"inbox_item":                f.fx.Count(t, "SELECT count(*) FROM inbox_item WHERE workspace_id=$1", f.workspace),
		"notification_bot_delivery": f.fx.Count(t, "SELECT count(*) FROM notification_bot_delivery d JOIN inbox_item i ON i.id=d.inbox_id WHERE i.workspace_id=$1", f.workspace),
	}
}
func newTaskActorChatFixture(t *testing.T) taskActorFixture {
	t.Helper()
	f := newTaskActorFixture(t, false)
	f.chat = f.fx.ChatSession(t, f.agent)
	f.fx.Exec(t, "UPDATE agent_task_queue SET issue_id=NULL,chat_session_id=$2 WHERE id=$1", f.task, f.chat)
	f.fx.Cleanup(t, "DELETE FROM chat_message WHERE chat_session_id=$1", f.chat)
	return f
}
func (f taskActorFixture) assertNoEffects(t *testing.T, op string) {
	t.Helper()
	for table, count := range f.effectCounts(t) {
		if count != 0 {
			t.Fatalf("stale actor created %s: %d", table, count)
		}
	}
	if op != "human" {
		var assignee string
		f.fx.QueryRow(t, "SELECT COALESCE(assignee_type,'') FROM issue WHERE id=$1", f.issue).Scan(&assignee)
		if assignee != "" {
			t.Fatal("stale actor assigned issue")
		}
		var handled int64
		f.fx.QueryRow(t, "SELECT handled_version FROM project_supervision WHERE project_id=$1", f.project).Scan(&handled)
		if handled != 0 {
			t.Fatal("stale actor reported handled version")
		}
		var mutations int
		f.fx.QueryRow(t, "SELECT COALESCE((context->>'verified_actions')::int,0)+COALESCE((context->>'requested_actions')::int,0) FROM agent_task_queue WHERE id=$1", f.task).Scan(&mutations)
		if mutations != 0 {
			t.Fatal("stale actor changed action context")
		}
	}
}
func TestTaskActorFreshClaimRetiresOldCredential(t *testing.T) {
	for _, op := range []string{"human", "actions", "report"} {
		t.Run(op, func(t *testing.T) {
			f := newTaskActorFixture(t, op != "human")
			old := f.token
			f.fx.Exec(t, "UPDATE agent_task_queue SET status='dispatched',started_at=NULL,dispatched_at=dispatched_at+interval '1 microsecond' WHERE id=$1", f.task)
			fresh := f.mint(t)
			f.fx.Exec(t, "UPDATE agent_task_queue SET status='running',started_at=now() WHERE id=$1", f.task)
			testutil.Call(t, f.router.ServeHTTP, actorRequest("POST", f.path(op), old, f.body(op))).Want(401)
			f.assertNoEffects(t, op)
			want := 200
			if op == "human" {
				want = 201
			}
			testutil.Call(t, f.router.ServeHTTP, actorRequest("POST", f.path(op), fresh, f.body(op))).Want(want)
			if op != "actions" {
				testutil.Call(t, f.router.ServeHTTP, actorRequest("POST", f.path(op), fresh, f.body(op))).Want(want)
			}
			if count := f.fx.Count(t, "SELECT count(*) FROM task_actor_claim WHERE task_id=$1", f.task); count != 2 {
				t.Fatal("immutable actor claim history missing")
			}
		})
	}
}
func TestTaskActorAuthenticatedRequestRechecksCredential(t *testing.T) {
	for _, op := range []string{"human", "actions", "report"} {
		for _, mode := range []string{"reclaim", "revoke", "expiry", "member", "execution", "cancel"} {
			t.Run(op+"/"+mode, func(t *testing.T) {
				f := newTaskActorFixture(t, op != "human")
				reached, release := make(chan struct{}), make(chan struct{})
				defer func() {
					select {
					case <-release:
					default:
						close(release)
					}
				}()
				router := taskActorRouter(testHandler, func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(reached); <-release; next.ServeHTTP(w, r) })
				})
				done := make(chan *httptest.ResponseRecorder, 1)
				go func() {
					response := httptest.NewRecorder()
					router.ServeHTTP(response, actorRequest("POST", f.path(op), f.token, f.body(op)))
					done <- response
				}()
				select {
				case <-reached:
				case <-time.After(3 * time.Second):
					t.Fatal("authentication not reached")
				}
				switch mode {
				case "reclaim":
					f.fx.Exec(t, "UPDATE agent_task_queue SET dispatched_at=dispatched_at+interval '1 microsecond' WHERE id=$1", f.task)
				case "revoke":
					f.fx.Exec(t, "DELETE FROM task_token WHERE token_hash=$1", auth.HashToken(f.token))
				case "expiry":
					f.fx.Exec(t, "UPDATE task_token SET expires_at=clock_timestamp()-interval '1 second' WHERE token_hash=$1", auth.HashToken(f.token))
				case "member":
					f.fx.Exec(t, "DELETE FROM member WHERE user_id=$1 AND workspace_id=$2", f.user, f.workspace)
				case "execution":
					f.fx.Exec(t, "INSERT INTO task_execution(task_id,execution_id,runtime_id,workspace_id,daemon_id,worker_id,dispatched_at,supervisor_epoch,revoked) SELECT id,$2,runtime_id,$3,'fixture',$4,dispatched_at,1,true FROM agent_task_queue WHERE id=$1", f.task, uuid.NewString(), f.workspace, uuid.NewString())
					f.fx.Cleanup(t, "DELETE FROM task_execution WHERE task_id=$1", f.task)
				case "cancel":
					f.fx.Exec(t, "UPDATE agent_task_queue SET status='cancelled',completed_at=now() WHERE id=$1", f.task)
				}
				close(release)
				select {
				case response := <-done:
					want := 409
					if mode == "member" {
						want = 404
					} else if mode == "cancel" && op != "human" {
						want = 403
					}
					if response.Code != want {
						t.Fatalf("stale actor admitted: %d %s", response.Code, response.Body.String())
					}
				case <-time.After(3 * time.Second):
					t.Fatal("actor request hung")
				}
				f.assertNoEffects(t, op)
			})
		}
	}
}
func TestTaskActorRejectsUnboundDowngradeAndMissingContext(t *testing.T) {
	f := newTaskActorFixture(t, false)
	legacy := "mat_" + strings.Repeat("a", 40)
	_, err := testHandler.Queries.CreateTaskToken(t.Context(), db.CreateTaskTokenParams{TokenHash: auth.HashToken(legacy), TaskID: parseUUID(f.task), AgentID: parseUUID(f.agent), WorkspaceID: parseUUID(f.workspace), UserID: parseUUID(f.user), ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, f.router.ServeHTTP, actorRequest("POST", f.path("human"), legacy, f.body("human"))).Want(409)
	request := newRequest("POST", f.path("human"), f.body("human"))
	request.Header.Set("X-Actor-Source", "task_token")
	request.Header.Set("X-Task-ID", f.task)
	request.Header.Set("X-Agent-ID", f.agent)
	request = request.WithContext(auth.WithTrustedInternalTaskActor(request.Context()))
	testutil.Call(t, testHandler.CreateHumanRequest, request).Want(403)
	source, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(f.task))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = testHandler.TaskService.CreateHumanRequest(context.Background(), source, parseUUID(f.workspace), f.body("human").(service.HumanRequestInput)); err == nil {
		t.Fatal("missing service auth context was trusted")
	}
}

func TestTaskActorStaleFinalizerAndMintFailurePreserveCurrentToken(t *testing.T) {
	f := newTaskActorFixture(t, false)
	f.fx.Exec(t, "UPDATE agent_task_queue SET status='dispatched',started_at=NULL WHERE id=$1", f.task)
	old, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(f.task))
	if err != nil {
		t.Fatal(err)
	}
	f.fx.Exec(t, "UPDATE agent_task_queue SET dispatched_at=dispatched_at+interval '1 microsecond' WHERE id=$1", f.task)
	currentToken := f.mint(t)
	params := func(hash string) db.CreateTaskTokenParams {
		return db.CreateTaskTokenParams{TokenHash: hash, TaskID: old.ID, AgentID: old.AgentID, WorkspaceID: parseUUID(f.workspace), UserID: parseUUID(f.user), ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}}
	}
	staleHash := auth.HashToken("fixture-stale-finalizer-" + uuid.NewString())
	if _, err = testHandler.TaskService.FinalizeTaskClaim(t.Context(), old, params(staleHash), nil, false, nil, nil); err == nil {
		t.Fatal("stale finalizer minted into new claim")
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM task_token WHERE token_hash=$1", staleHash); count != 0 {
		t.Fatal("stale token persisted")
	}
	if _, err = testHandler.Queries.GetTaskTokenByHash(t.Context(), auth.HashToken(currentToken)); err != nil {
		t.Fatal("stale finalizer retired current token")
	}
	current, err := testHandler.Queries.GetAgentTask(t.Context(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.fx.Exec(t, "UPDATE agent_task_queue SET dispatched_at=dispatched_at+interval '1 microsecond' WHERE id=$1", f.task)
	next, err := testHandler.Queries.GetAgentTask(t.Context(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	failedHash := auth.HashToken("fixture-failed-finalizer-" + uuid.NewString())
	svc := &service.TaskService{Queries: testHandler.Queries, TxStarter: rollbackOnCommitTxStarter{pool: testPool}}
	if _, err = svc.FinalizeTaskClaim(t.Context(), next, params(failedHash), nil, false, nil, nil); err == nil {
		t.Fatal("forced rollback succeeded")
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM task_token WHERE token_hash=$1", failedHash); count != 0 {
		t.Fatal("failed token persisted")
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM task_actor_claim WHERE token_hash=$1", failedHash); count != 0 {
		t.Fatal("failed binding persisted")
	}
	if _, err = testHandler.Queries.GetTaskTokenByHash(t.Context(), auth.HashToken(currentToken)); err != nil {
		t.Fatal("failed finalizer retired prior token outside transaction")
	}
	binding, err := testHandler.Queries.GetTaskActorClaim(t.Context(), auth.HashToken(currentToken))
	if err != nil || !binding.DispatchedAt.Time.Equal(current.DispatchedAt.Time) {
		t.Fatal("existing binding mutated")
	}
}

func waitActorTaskLock(t *testing.T) {
	t.Helper()
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := testPool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '-- name: LockVscreenSourceTask%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("actor did not reach source-task lock")
		case <-ticker.C:
		}
	}
}
func TestTaskActorWaitedTaskLockRevalidatesDispatchAndExpiry(t *testing.T) {
	for _, op := range []string{"human", "actions", "report"} {
		for _, mode := range []string{"reclaim", "expiry", "revoke", "cancel"} {
			t.Run(op+"/"+mode, func(t *testing.T) {
				f := newTaskActorFixture(t, op != "human")
				if mode == "expiry" {
					f.fx.Exec(t, "UPDATE task_token SET expires_at=clock_timestamp()+interval '400 milliseconds' WHERE token_hash=$1", auth.HashToken(f.token))
				}
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
					f.router.ServeHTTP(response, actorRequest("POST", f.path(op), f.token, f.body(op)))
					done <- response
				}()
				waitActorTaskLock(t)
				switch mode {
				case "reclaim":
					_, err = tx.Exec(t.Context(), "UPDATE agent_task_queue SET dispatched_at=dispatched_at+interval '1 microsecond' WHERE id=$1", f.task)
				case "revoke":
					_, err = tx.Exec(t.Context(), "DELETE FROM task_token WHERE token_hash=$1", auth.HashToken(f.token))
				case "cancel":
					_, err = tx.Exec(t.Context(), "UPDATE agent_task_queue SET status='cancelled',completed_at=now() WHERE id=$1", f.task)
				case "expiry":
					deadline := time.NewTimer(3 * time.Second)
					defer deadline.Stop()
					ticker := time.NewTicker(5 * time.Millisecond)
					defer ticker.Stop()
					for {
						var expired bool
						f.fx.QueryRow(t, "SELECT expires_at<clock_timestamp() FROM task_token WHERE token_hash=$1", auth.HashToken(f.token)).Scan(&expired)
						if expired {
							break
						}
						select {
						case <-deadline.C:
							t.Fatal("expiry did not advance")
						case <-ticker.C:
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
				case response := <-done:
					want := 409
					if mode == "cancel" && op != "human" {
						want = 403
					}
					if response.Code != want {
						t.Fatalf("waited actor accepted: %d %s", response.Code, response.Body.String())
					}
				case <-time.After(3 * time.Second):
					t.Fatal("actor remained blocked")
				}
				f.assertNoEffects(t, op)
			})
		}
	}
}
func TestTaskActorPrincipalContentionFailsClosedWithoutDeadlock(t *testing.T) {
	for _, op := range []string{"human", "actions", "report"} {
		t.Run(op, func(t *testing.T) {
			f := newTaskActorFixture(t, op != "human")
			tx, err := testPool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(t.Context(), "SELECT id FROM agent_runtime WHERE id=$1 FOR UPDATE", f.runtime); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			request := actorRequest("POST", f.path(op), f.token, f.body(op)).WithContext(ctx)
			started := time.Now()
			testutil.Call(t, f.router.ServeHTTP, request).Want(409)
			if time.Since(started) > time.Second {
				t.Fatal("principal lock waited instead of failing closed")
			}
			if _, err = tx.Exec(ctx, "SELECT id FROM agent_task_queue WHERE id=$1 FOR UPDATE NOWAIT", f.task); err != nil {
				t.Fatal("actor leaked task lock")
			}
			f.assertNoEffects(t, op)
			if err = tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			want := 200
			if op == "human" {
				want = 201
			}
			testutil.Call(t, f.router.ServeHTTP, actorRequest("POST", f.path(op), f.token, f.body(op))).Want(want)
		})
	}
}
func TestTaskActorLegacyLaneAndOptionalExecutionBinding(t *testing.T) {
	legacy := newTaskActorFixture(t, false, true)
	testutil.Call(t, legacy.router.ServeHTTP, actorRequest("POST", legacy.path("human"), legacy.token, legacy.body("human"))).Want(201)
	if count := legacy.fx.Count(t, "SELECT count(*) FROM task_actor_claim WHERE task_id=$1", legacy.task); count != 0 {
		t.Fatal("legacy credential was backfilled")
	}
	f := newTaskActorFixture(t, false)
	f.fx.Exec(t, "INSERT INTO task_execution(task_id,execution_id,runtime_id,workspace_id,daemon_id,worker_id,dispatched_at,supervisor_epoch) SELECT t.id,$2,t.runtime_id,$3,r.daemon_id,$4,t.dispatched_at,1 FROM agent_task_queue t JOIN agent_runtime r ON r.id=t.runtime_id WHERE t.id=$1", f.task, uuid.NewString(), f.workspace, uuid.NewString())
	f.fx.Cleanup(t, "DELETE FROM task_execution WHERE task_id=$1", f.task)
	testutil.Call(t, f.router.ServeHTTP, actorRequest("POST", f.path("human"), f.token, f.body("human"))).Want(201)
	f.fx.Exec(t, "UPDATE task_execution SET dispatched_at=dispatched_at+interval '1 microsecond' WHERE task_id=$1", f.task)
	input := f.body("human").(service.HumanRequestInput)
	input.Title = "must not revise"
	testutil.Call(t, f.router.ServeHTTP, actorRequest("POST", f.path("human"), f.token, input)).Want(409)
	var revision int64
	f.fx.QueryRow(t, "SELECT revision FROM human_request WHERE source_task_id=$1", f.task).Scan(&revision)
	if revision != 1 {
		t.Fatal("stale optional execution revised human request")
	}
}

func TestTaskActorExpiryBeforeCommitRollsBackAllEffects(t *testing.T) {
	for _, op := range []string{"human", "human_chat", "revise", "actions", "report"} {
		t.Run(op, func(t *testing.T) {
			var f taskActorFixture
			if op == "human_chat" {
				f = newTaskActorChatFixture(t)
			} else {
				f = newTaskActorFixture(t, op == "actions" || op == "report")
			}
			routeOp := op
			body := f.body(op)
			target := "CreateHumanRequest"
			var beforeEffects map[string]int
			if op == "human_chat" {
				routeOp = "human"
				body = f.body("human")
			}
			if op == "revise" {
				routeOp = "human"
				testutil.Call(t, f.router.ServeHTTP, actorRequest("POST", f.path("human"), f.token, f.body("human"))).Want(201)
				beforeEffects = f.effectCounts(t)
				input := f.body("human").(service.HumanRequestInput)
				input.Title = "expired revision must not commit"
				body = input
				target = "ReplaceHumanRequest"
			} else if op == "actions" {
				target = "SetTaskProjectContext"
			} else if op == "report" {
				target = "StoreProjectSupervisionResult"
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			starter := executionTransportPausedStarter{target: target, entered: entered, release: release}
			h := *testHandler
			tasks := &service.TaskService{Queries: testHandler.Queries, TxStarter: starter, Bus: testHandler.TaskService.Bus}
			h.TaskService = tasks
			h.ProjectSupervisionService = &service.ProjectSupervisionService{Tasks: tasks}
			router := taskActorRouter(&h, nil)
			f.fx.Exec(t, "UPDATE task_token SET expires_at=clock_timestamp()+interval '700 milliseconds' WHERE token_hash=$1", auth.HashToken(f.token))
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				response := httptest.NewRecorder()
				router.ServeHTTP(response, actorRequest("POST", f.path(routeOp), f.token, body))
				done <- response
			}()
			select {
			case <-entered:
			case response := <-done:
				t.Fatalf("write did not reach pause: %d %s", response.Code, response.Body.String())
			case <-time.After(5 * time.Second):
				t.Fatal("write did not reach pause")
			}
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				var expired bool
				f.fx.QueryRow(t, "SELECT expires_at<clock_timestamp() FROM task_token WHERE token_hash=$1", auth.HashToken(f.token)).Scan(&expired)
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
					t.Fatalf("expired effects committed: %d %s", response.Code, response.Body.String())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("expired callback blocked")
			}
			if op == "revise" {
				var revision int64
				f.fx.QueryRow(t, "SELECT revision FROM human_request WHERE source_task_id=$1", f.task).Scan(&revision)
				for table, count := range f.effectCounts(t) {
					if count != beforeEffects[table] {
						t.Fatalf("expired revision changed %s rows", table)
					}
				}
				if revision != 1 {
					t.Fatal("expired revision committed")
				}
			} else {
				f.assertNoEffects(t, routeOp)
			}
		})
	}
}
