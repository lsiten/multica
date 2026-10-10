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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// taskUsageFaultTxStarter stands in for any failure while storing reported usage:
// ReportTaskUsage now writes through beginExecutionCallback's transaction, so the
// fault has to intercept the transaction's Exec, not the handler's Queries.
type taskUsageFaultTxStarter struct {
	delegate *pgxpool.Pool
	writes   int
	failAt   int
}

type taskUsageFaultTx struct {
	pgx.Tx
	starter *taskUsageFaultTxStarter
}

func (s *taskUsageFaultTxStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.delegate.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return taskUsageFaultTx{Tx: tx, starter: s}, nil
}

func (t taskUsageFaultTx) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(query, "-- name: UpsertTaskUsage :exec") {
		t.starter.writes++
		if t.starter.writes == t.starter.failAt {
			return pgconn.CommandTag{}, errors.New("injected task usage write failure")
		}
	}
	return t.Tx.Exec(ctx, query, args...)
}

func TestReportTaskUsage_WriteFailureAndReplay(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("write_%d", failAt), func(t *testing.T) {
			runtimeID := dbfx.Runtime(t, "Usage report runtime")
			agentID := dbfx.Agent(t, "Usage report agent", runtimeID)
			issueID := dbfx.Issue(t, "Usage report issue")
			taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "status": "completed"})
			t.Cleanup(func() { dbfx.Exec(t, "DELETE FROM task_usage WHERE task_id = $1", taskID) })
			fault := &taskUsageFaultTxStarter{delegate: testPool, failAt: failAt}
			h := *testHandler
			h.TxStarter = fault
			usage := []TaskUsagePayload{
				{Provider: "codex", Model: "model-a", InputTokens: 100, OutputTokens: 20, CacheReadTokens: 50},
				{Provider: "codex", Model: "model-b", InputTokens: 30, OutputTokens: 5, CacheWriteTokens: 10, CostUSDTicks: 900},
			}
			request := func(want int) {
				req := newRequest(http.MethodPost, "/api/daemon/tasks/"+taskID+"/usage", map[string]any{"usage": usage})
				req = withURLParam(req, "taskId", taskID)
				testutil.Call(t, h.ReportTaskUsage, req).Want(want)
			}
			request(http.StatusInternalServerError)
			request(http.StatusOK)
			request(http.StatusOK)
			rows, err := testHandler.Queries.GetTaskUsage(context.Background(), parseUUID(taskID))
			if err != nil {
				t.Fatalf("read replayed usage: %v", err)
			}
			if len(rows) != len(usage) {
				t.Fatalf("stored models = %d, want %d", len(rows), len(usage))
			}
			for i, row := range rows {
				want := usage[i]
				if row.Provider != want.Provider || row.Model != want.Model || row.InputTokens != want.InputTokens || row.OutputTokens != want.OutputTokens || row.CacheReadTokens != want.CacheReadTokens || row.CacheWriteTokens != want.CacheWriteTokens || row.CostUsdTicks.Valid != (want.CostUSDTicks > 0) || row.CostUsdTicks.Int64 != want.CostUSDTicks {
					t.Errorf("stored usage = %+v, want %+v", row, want)
				}
			}
		})
	}
}
