package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// claimTask is the runtime-scoped claim primitive used by daemon poll paths.
// The exported ClaimTask wrapper omits runtimeID and therefore resolves the
// agent's currently bound runtime. Scoping the SQL claim itself prevents an
// offline candidate on runtime A from causing the same agent's task on runtime
// B to be dispatched and then dropped by the caller's runtime guard.
func (s *TaskService) claimTask(ctx context.Context, agentID, runtimeID pgtype.UUID) (*db.AgentTaskQueue, error) {
	start := time.Now()
	outcome := "unknown"
	var getAgentMs, countRunningMs, claimAgentMs, reanchorMs, updateStatusMs, dispatchMs int64
	var claimed *db.AgentTaskQueue
	var rejectedCoordination *db.AgentTaskQueue
	var reclaimCheckAfter time.Time
	defer func() {
		s.maybeLogClaimSlow(agentID, outcome, start, getAgentMs, countRunningMs, claimAgentMs, reanchorMs, updateStatusMs, dispatchMs)
	}()

	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		t0 := time.Now()
		agent, err := qtx.GetAgentForTaskClaim(ctx, agentID)
		getAgentMs = time.Since(t0).Milliseconds()
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = "agent_unavailable"
			return nil
		}
		if err != nil {
			outcome = "error_get_agent"
			return fmt.Errorf("agent not found: %w", err)
		}
		claimRuntimeID := runtimeID
		if !claimRuntimeID.Valid {
			claimRuntimeID = agent.RuntimeID
		}
		if !claimRuntimeID.Valid {
			outcome = "no_runtime"
			return nil
		}
		// A daemon may still hold a stale candidate after the agent is rebound.
		// Reject it before doing capacity work. ClaimAgentTask repeats the fence
		// before its state transition; the claim handler then rechecks the freshly
		// loaded Agent before returning any payload. Runtime mutation teardown is
		// responsible for serializing and settling the remaining queued rows.
		if runtimeID.Valid && agent.RuntimeID != runtimeID {
			outcome = "runtime_mismatch"
			return nil
		}

		t0 = time.Now()
		running, err := qtx.CountRunningTasks(ctx, agentID)
		countRunningMs = time.Since(t0).Milliseconds()
		if err != nil {
			outcome = "error_count_running"
			return fmt.Errorf("count running tasks: %w", err)
		}
		if running >= int64(agent.MaxConcurrentTasks) {
			slog.Debug("task claim: no capacity", "agent_id", util.UUIDToString(agentID), "running", running, "max", agent.MaxConcurrentTasks)
			outcome = "no_capacity"
			return nil
		}

		t0 = time.Now()
		reclaimCheckAfter = t0.Add(claimResponseRecoveryWindow + ReclaimCheckHintSafetyMargin)
		task, err := qtx.ClaimAgentTask(ctx, db.ClaimAgentTaskParams{
			AgentID:          agentID,
			RuntimeID:        claimRuntimeID,
			PrepareLeaseSecs: prepareLeaseDuration.Seconds(),
			RuntimeStaleSecs: RuntimeClaimFreshnessSeconds,
		})
		claimAgentMs = time.Since(t0).Milliseconds()
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				slog.Debug("task claim: no tasks available", "agent_id", util.UUIDToString(agentID))
				outcome = "no_tasks"
				return nil
			}
			outcome = "error_claim"
			return fmt.Errorf("claim task: %w", err)
		}

		if err = s.checkProjectAdmission(ctx, qtx, task); err != nil {
			if errors.Is(err, ErrProjectSupervisionForbidden) {
				rejectedCoordination = &task
			}
			return err
		}

		// An idle task-owned direct-chat row may already be visible as the
		// positional queue head. Normal completion reanchors a successor beside
		// the assistant outcome before commit; this claim-time query is the
		// compatibility fallback for an older or otherwise out-of-order row.
		// It only moves that row inside the claim transaction, so full-list and
		// cursor readers see the corrected boundary once it is dispatched. Retry
		// children keep the root input owner and stale-dispatch reclaim uses a
		// separate query, so neither path moves an already-visible turn again.
		if task.ChatSessionID.Valid && task.ChatInputTaskID.Valid && task.ChatInputTaskID == task.ID {
			t0 = time.Now()
			if err := qtx.ReanchorClaimedDirectChatInput(ctx, db.ReanchorClaimedDirectChatInputParams{
				DispatchedAt: task.DispatchedAt,
				TaskID:       task.ID,
			}); err != nil {
				reanchorMs = time.Since(t0).Milliseconds()
				outcome = "error_reanchor_chat_input"
				return fmt.Errorf("reanchor claimed direct chat input: %w", err)
			}
			reanchorMs = time.Since(t0).Milliseconds()
		}

		claimedTask := task
		claimed = &claimedTask
		return nil
	})
	if err != nil {
		if rejectedCoordination != nil {
			if _, cancelErr := s.CancelTaskWithReason(ctx, rejectedCoordination.ID, "project coordination authority changed", "scope_changed"); cancelErr != nil {
				return nil, cancelErr
			}
			return nil, nil
		}
		if errors.Is(err, ErrProjectExecutionCapacity) {
			return nil, nil
		}
		if outcome == "unknown" {
			outcome = "error_transaction"
		}
		return nil, err
	}
	if claimed == nil {
		return nil, nil
	}
	s.trackTaskForReclaim(*claimed, reclaimCheckAfter)

	slog.Info("task claimed", "task_id", util.UUIDToString(claimed.ID), "agent_id", util.UUIDToString(agentID))
	s.captureTaskDispatched(ctx, *claimed)

	// Refresh agent status from active tasks. This avoids a stale unconditional
	// working write racing after a just-cancelled claim.
	t0 := time.Now()
	s.ReconcileAgentStatus(ctx, agentID)
	updateStatusMs = time.Since(t0).Milliseconds()

	// Broadcast task:dispatch. ResolveTaskWorkspaceID inside this path can
	// re-query issue/chat_session/autopilot_run, so it can also be a real
	// contributor to claim latency.
	t0 = time.Now()
	s.broadcastTaskDispatch(ctx, *claimed)
	dispatchMs = time.Since(t0).Milliseconds()

	outcome = "claimed"
	return claimed, nil
}
