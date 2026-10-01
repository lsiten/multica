package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func jevTestHandler(t *testing.T) *Handler {
	t.Helper()
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	// A temporary table exercises the actual SQL without migrating or mutating
	// the checkout's running database. Public workspace/task fixtures stay scoped.
	_, err = tx.Exec(context.Background(), `CREATE TEMP TABLE workspace_jev_config (workspace_id uuid PRIMARY KEY, config jsonb NOT NULL, revision bigint NOT NULL DEFAULT 1, updated_at timestamptz NOT NULL DEFAULT now()) ON COMMIT DROP`)
	if err != nil {
		t.Fatal(err)
	}
	h := *testHandler
	h.Queries = db.New(tx)
	h.TxStarter = tx
	h.DB = tx
	return &h
}
func jevRequest(method string, body any) *http.Request {
	return withURLParams(newRequest(method, "/api/workspaces/"+testWorkspaceID+"/jev-config", body), "id", testWorkspaceID)
}
func TestWorkspaceJevConfigRoundTripAndOptimisticRevision(t *testing.T) {
	h := jevTestHandler(t)
	var initial workspaceJevConfigResponse
	testutil.Call(t, h.GetWorkspaceJevConfig, jevRequest(http.MethodGet, nil)).Want(200).JSON(&initial)
	if initial.Revision != 0 || initial.Config.Source != "agent_context" {
		t.Fatalf("default=%+v", initial)
	}
	cfg := protocol.DefaultWorkspaceJevConfig()
	var saved workspaceJevConfigResponse
	testutil.Call(t, h.UpdateWorkspaceJevConfig, jevRequest(http.MethodPut, updateWorkspaceJevConfigRequest{Config: cfg})).Want(200).JSON(&saved)
	if saved.Revision != 1 {
		t.Fatalf("revision=%d", saved.Revision)
	}
	cfg.TimeoutSeconds = 90
	testutil.Call(t, h.UpdateWorkspaceJevConfig, jevRequest(http.MethodPut, updateWorkspaceJevConfigRequest{Config: cfg, Revision: 1})).Want(200).JSON(&saved)
	if saved.Revision != 2 || saved.Config.TimeoutSeconds != 90 {
		t.Fatalf("update=%+v", saved)
	}
	for _, stale := range []int64{0, 1, 99} {
		testutil.Call(t, h.UpdateWorkspaceJevConfig, jevRequest(http.MethodPut, updateWorkspaceJevConfigRequest{Config: cfg, Revision: stale})).Want(409)
	}
	testutil.Call(t, h.GetWorkspaceJevConfig, jevRequest(http.MethodGet, nil)).Want(200).JSON(&saved)
	if saved.Revision != 2 {
		t.Fatal(saved.Revision)
	}
}
func TestWorkspaceJevRejectsMalformedAndMachineWrites(t *testing.T) {
	h := jevTestHandler(t)
	for _, body := range []string{`null`, `{} {}`, `{"revision":-1,"config":{"source":"agent_context","timeout_seconds":45}}`, `{"revision":0,"config":{"source":"agent_context","timeout_seconds":45},"extra":true}`} {
		testutil.Call(t, h.UpdateWorkspaceJevConfig, jevRequest(http.MethodPut, body)).Want(400)
	}
	r := jevRequest(http.MethodPut, updateWorkspaceJevConfigRequest{Config: protocol.DefaultWorkspaceJevConfig()})
	r.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, h.UpdateWorkspaceJevConfig, r).Want(403)
}
func TestWorkspaceJevSnapshotPersistsRevisionAndRejectsInvalidContext(t *testing.T) {
	agentID := dbfx.Agent(t, "Jev snapshot", testRuntimeID)
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": testRuntimeID, "status": "dispatched"})
	h := jevTestHandler(t)
	config := protocol.DefaultWorkspaceJevConfig()
	var saved workspaceJevConfigResponse
	testutil.Call(t, h.UpdateWorkspaceJevConfig, jevRequest(http.MethodPut, updateWorkspaceJevConfigRequest{Config: config})).Want(200).JSON(&saved)
	task := db.AgentTaskQueue{ID: parseUUID(taskID), AgentID: parseUUID(agentID)}
	first, err := h.captureWorkspaceJevConfig(t.Context(), task, parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 {
		t.Fatalf("snapshot revision=%d", first.Revision)
	}
	config.TimeoutSeconds = 120
	testutil.Call(t, h.UpdateWorkspaceJevConfig, jevRequest(http.MethodPut, updateWorkspaceJevConfigRequest{Config: config, Revision: 1})).Want(200)
	// Replay even a stale in-memory task; the DB's first snapshot must win.
	second, err := h.captureWorkspaceJevConfig(t.Context(), task, parseUUID(testWorkspaceID))
	if err != nil || second != first {
		t.Fatalf("replay=%+v err=%v", second, err)
	}
	var persisted []byte
	if err = h.DB.QueryRow(t.Context(), `SELECT context FROM agent_task_queue WHERE id=$1`, task.ID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	var contextMap map[string]json.RawMessage
	if json.Unmarshal(persisted, &contextMap) != nil || len(contextMap["workspace_jev"]) == 0 {
		t.Fatal("snapshot absent")
	}
	task.Context = []byte(`{"workspace_jev":null}`)
	if _, err = h.captureWorkspaceJevConfig(t.Context(), task, parseUUID(testWorkspaceID)); err == nil {
		t.Fatal("invalid snapshot silently accepted")
	}
}
