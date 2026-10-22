package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestBatchClaimFillsEachAgentsOwnCapacity(t *testing.T) {
	pool := sharedTestPool(t)
	fx := testutil.New(pool, "", "")
	suffix := fmt.Sprint(time.Now().UnixNano())
	fx.UserID = fx.User(t, "Claim isolation", "claim-isolation-"+suffix+"@test.local")
	fx.WorkspaceID = fx.Workspace(t, "Claim isolation", "claim-isolation-"+suffix)
	fx.Member(t, fx.WorkspaceID, fx.UserID, "owner")
	runtime := fx.Runtime(t, "Shared runtime")
	withCapacity := fx.Agent(t, "Two of six", runtime, testutil.Cols{"max_concurrent_tasks": 6})
	full := fx.Agent(t, "Full", runtime, testutil.Cols{"max_concurrent_tasks": 1})
	independent := fx.Agent(t, "Independent", runtime, testutil.Cols{"max_concurrent_tasks": 2})
	for _, agent := range []string{withCapacity, withCapacity, full} {
		fx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "status": "running", "issue_id": fx.Issue(t, "Active issue")})
	}
	for _, agent := range []string{withCapacity, withCapacity, withCapacity, withCapacity, full, independent, independent} {
		fx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": fx.Issue(t, "Queued issue")})
	}
	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	claimed, err := svc.ClaimTasksForRuntimes(t.Context(), []pgtype.UUID{util.MustParseUUID(runtime)}, 20)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, task := range claimed {
		counts[util.UUIDToString(task.AgentID)]++
	}
	if len(claimed) != 6 || counts[withCapacity] != 4 || counts[full] != 0 || counts[independent] != 2 {
		t.Fatalf("free agent capacity was not filled independently: claimed=%d, counts=%v", len(claimed), counts)
	}
	for agent, want := range map[string]int{withCapacity: 6, full: 1, independent: 2} {
		if got := fx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND status IN ('dispatched','running','waiting_local_directory')`, agent); got != want {
			t.Fatalf("agent %s active count=%d, want %d", agent, got, want)
		}
	}
}

func TestBatchClaimSkipsLockedAgentWithoutBlockingPeers(t *testing.T) {
	pool := sharedTestPool(t)
	fx := testutil.New(pool, "", "")
	suffix := fmt.Sprint(time.Now().UnixNano())
	fx.UserID = fx.User(t, "Locked claim", "locked-claim-"+suffix+"@test.local")
	fx.WorkspaceID = fx.Workspace(t, "Locked claim", "locked-claim-"+suffix)
	fx.Member(t, fx.WorkspaceID, fx.UserID, "owner")
	runtime := fx.Runtime(t, "Shared runtime")
	locked := fx.Agent(t, "Locked agent", runtime)
	peer := fx.Agent(t, "Healthy agent", runtime)
	fx.Task(t, locked, testutil.Cols{"runtime_id": runtime, "priority": 10, "issue_id": fx.Issue(t, "Locked issue")})
	peerTask := fx.Task(t, peer, testutil.Cols{"runtime_id": runtime, "issue_id": fx.Issue(t, "Healthy issue")})
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := db.New(tx).GetAgentForClaimUpdate(t.Context(), util.MustParseUUID(locked)); err != nil {
		t.Fatal(err)
	}
	svc := NewTaskService(db.New(pool), pool, nil, events.New())
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	claimed, err := svc.ClaimTasksForRuntimes(ctx, []pgtype.UUID{util.MustParseUUID(runtime)}, 5)
	if err != nil || len(claimed) != 1 || util.UUIDToString(claimed[0].ID) != peerTask {
		t.Fatalf("one locked agent blocked its peer: claimed=%v, err=%v", claimed, err)
	}
}

func TestBatchClaimFailedAgentDoesNotBlockPeers(t *testing.T) {
	pool := sharedTestPool(t)
	rt1, rt2 := batchClaimFixture(t, t.Context(), pool)
	svc := NewTaskService(db.New(pool), &failNthBeginTxStarter{inner: pool, failOn: 1}, nil, events.New())
	claimed, err := svc.ClaimTasksForRuntimes(t.Context(), []pgtype.UUID{util.MustParseUUID(rt1), util.MustParseUUID(rt2)}, 5)
	if err != nil || len(claimed) != 1 || util.UUIDToString(claimed[0].RuntimeID) != rt2 {
		t.Fatalf("one failed agent blocked its peer: claimed=%v, err=%v", claimed, err)
	}
}
