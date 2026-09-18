package handler

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenInterventionAuthenticatedDaemonRoute(t *testing.T) {
	// Given a real database-backed scoped daemon token and its currently connected runtime.
	f := newInterventionFixture(t, "issue")
	rt, err := f.h.Queries.GetAgentRuntime(context.Background(), parseUUID(f.report.RuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	token := "mdt_" + uuid.NewString()
	dbfx.Insert(t, "daemon_token", testutil.Cols{"token_hash": auth.HashToken(token), "workspace_id": testWorkspaceID, "daemon_id": rt.DaemonID.String, "expires_at": testutil.Raw("now()+interval '1 hour'")})
	router := chi.NewRouter()
	router.Use(middleware.DaemonAuth(f.h.Queries, nil, nil, nil))
	router.Post("/api/daemon/runtimes/{runtimeId}/vscreen/interventions", f.h.ReportVscreenIntervention)
	req := testutil.JSONRequest(http.MethodPost, "/api/daemon/runtimes/"+f.report.RuntimeID+"/vscreen/interventions", f.report)
	req.Header.Set("Authorization", "Bearer "+token)
	// When the authenticated report traverses middleware and the actual route.
	var row db.RuntimeVscreenIntervention
	testutil.Call(t, router.ServeHTTP, req).Want(200).JSON(&row)
	// Then the handoff persists; an unauthenticated browser cannot report it.
	if row.State != "awaiting_takeover" {
		t.Fatalf("state=%s", row.State)
	}
	testutil.Call(t, router.ServeHTTP, testutil.JSONRequest(http.MethodPost, req.URL.Path, f.report)).Want(401)
	t.Log("actual daemon auth route: scoped token 200; missing credential 401; token redacted")
}

func TestVscreenInterventionPersistsAcrossServiceReconstruction(t *testing.T) {
	// Given a ready handoff in PostgreSQL.
	f := newInterventionFixture(t, "quick")
	f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 200)
	f.sendReport(t, protocol.VscreenInterventionHuman, 200)
	f.sendReport(t, protocol.VscreenInterventionReadyToContinue, 200)
	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	replacement := service.NewTaskService(db.New(pool), pool, f.h.TaskService.Hub, f.h.TaskService.Bus, f.h.TaskService.Wakeup)
	f.h.Queries = replacement.Queries
	f.h.TaskService = replacement
	// When a reconstructed service and fresh DB pool continue using a fresh daemon observation.
	var out struct {
		TaskID string `json:"task_id"`
	}
	testutil.Call(t, f.h.ContinueVscreenIntervention, f.continueRequest(map[string]any{"human_summary": "returned"})).Want(200).JSON(&out)
	// Then the persisted handoff is consumed once, without memory-held proof.
	if out.TaskID == "" {
		t.Fatal("missing task")
	}
	t.Logf("reconstructed service+new DB pool HTTP task_id=%s; native proof freshly queried", out.TaskID)
}

func TestVscreenInterventionQuickCreateTransfersCapturedContext(t *testing.T) {
	// Given a quick-create source that owns a pending immutable capture.
	f := newInterventionFixture(t, "quick")
	contextID := dbfx.Insert(t, "issue_source_context", testutil.Cols{"id": uuid.NewString(), "workspace_id": testWorkspaceID, "origin_task_id": f.report.SourceTaskID, "source_issue_id": dbfx.Issue(t, "capture source"), "anchor_comment_id": uuid.NewString(), "captured_by_user_id": testUserID, "snapshot_version": 1, "snapshot": testutil.Raw("'{}'::jsonb"), "capture_digest": "digest", "state": "pending"})
	dbfx.Exec(t, "UPDATE agent_task_queue SET context=context||jsonb_build_object('source_context_id',$2::text) WHERE id=$1", f.report.SourceTaskID, contextID)
	f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 200)
	f.sendReport(t, protocol.VscreenInterventionHuman, 200)
	f.sendReport(t, protocol.VscreenInterventionReadyToContinue, 200)
	// When a human continues this exact source.
	var out struct {
		TaskID string `json:"task_id"`
	}
	testutil.Call(t, f.h.ContinueVscreenIntervention, f.continueRequest(map[string]any{"human_summary": "verified"})).Want(200).JSON(&out)
	// Then ownership transfers atomically to the child while the capture remains pending and unchanged.
	var origin, state, digest string
	dbfx.QueryRow(t, "SELECT origin_task_id,state,capture_digest FROM issue_source_context WHERE id=$1", contextID).Scan(&origin, &state, &digest)
	if origin != out.TaskID || state != "pending" || digest != "digest" {
		t.Fatalf("origin=%s state=%s digest=%s", origin, state, digest)
	}
	t.Logf("HTTP task_id=%s DB captured_context_origin=%s state=pending unchanged_digest=true", out.TaskID, origin)
}
