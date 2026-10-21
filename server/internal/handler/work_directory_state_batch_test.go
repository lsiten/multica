package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type directoryBatchBudget struct {
	txStarter
	writes int
}

type directoryBatchBudgetTx struct {
	pgx.Tx
	budget *directoryBatchBudget
}

func (b *directoryBatchBudget) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := b.txStarter.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return directoryBatchBudgetTx{Tx: tx, budget: b}, nil
}

func (t directoryBatchBudgetTx) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(query, "-- name: UpdateRuntimeWorkDirectoryState") {
		t.budget.writes++
		if t.budget.writes > 1 {
			return pgconn.CommandTag{}, errors.New("directory batch exhausted database round-trip budget")
		}
	}
	return t.Tx.Exec(ctx, query, args...)
}

func TestRuntimeWorkDirectoryStatePersistsFullPageWithinOneDatabaseWrite(t *testing.T) {
	runtime := dbfx.Runtime(t, "directory-page", testutil.Cols{"daemon_id": "page-daemon", "runtime_mode": "local"})
	agent := dbfx.Agent(t, "directory-page-agent", runtime)
	taskID := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "status": "completed", "work_dir": "/expired/499"})
	restored := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "status": "completed", "work_dir": "/expired/000", "work_dir_missing": true})
	states := make([]protocol.WorkDirectoryState, 500)
	for i := range states {
		states[i] = protocol.WorkDirectoryState{Path: fmt.Sprintf("/expired/%03d", i), Missing: true}
	}
	states[0].Missing = false
	budget := &directoryBatchBudget{txStarter: testHandler.TxStarter}
	h := *testHandler
	h.TxStarter = budget
	r := withURLParam(newDaemonTokenRequest(http.MethodPost, "/work-directories", states, testWorkspaceID, "page-daemon"), "runtimeId", runtime)
	testutil.Call(t, h.UpdateRuntimeWorkDirectories, r).Want(http.StatusOK)
	task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(taskID))
	if err != nil || !task.WorkDirMissing {
		t.Fatalf("last entry in full page was not persisted: missing=%v, err=%v", task.WorkDirMissing, err)
	}
	task, err = testHandler.Queries.GetAgentTask(t.Context(), parseUUID(restored))
	if err != nil || task.WorkDirMissing {
		t.Fatalf("restored directory was not persisted: missing=%v, err=%v", task.WorkDirMissing, err)
	}
	if budget.writes != 1 {
		t.Fatalf("full directory page used %d database writes, want 1", budget.writes)
	}
}
