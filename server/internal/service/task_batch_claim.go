package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ClaimTasksForRuntimes is the machine-level (MUL-4257) batch counterpart of
// ClaimTaskForRuntime: it claims up to maxTasks tasks across every runtime in
// runtimeIDs in a single call, so a daemon can poll for all of its runtimes
// with one HTTP request and a constant number of DB queries instead of one
// request (and one promote/reclaim/list cycle) per runtime.
//
// It preserves the exact per-runtime semantics, just set-ified:
//  1. promote due deferred tasks across the set (one UPDATE);
//  2. reclaim up to maxTasks stale-dispatched tasks across the set (one UPDATE)
//     — done before the empty-cache check because a lost claim response moves
//     the task out of `queued`, which the empty-queued cache cannot represent;
//  3. short-circuit runtimes whose empty-claim verdict is cached, sampling the
//     invalidation version for the rest BEFORE the candidate SELECT;
//  4. list queued candidates across the non-empty set (one SELECT);
//  5. mark still-empty runtimes so their next idle poll skips Postgres;
//  6. claim round-robin by agent via the runtime-scoped helper, preserving
//     per-(issue, agent) serialization, the agent concurrency cap, and every
//     dispatch side effect until maxTasks is reached.
//
// The returned slice contains both reclaimed and freshly-claimed tasks, each
// already carrying its runtime_id so the daemon routes it to the matching
// runtime locally.
func (s *TaskService) ClaimTasksForRuntimes(ctx context.Context, runtimeIDs []pgtype.UUID, maxTasks int) ([]db.AgentTaskQueue, error) {
	if len(runtimeIDs) == 0 || maxTasks <= 0 {
		return nil, nil
	}

	// De-dup runtime IDs defensively so MarkEmpty/version bookkeeping stays
	// unambiguous even if a daemon ever sends a duplicate.
	seen := make(map[string]struct{}, len(runtimeIDs))
	uniqueIDs := make([]pgtype.UUID, 0, len(runtimeIDs))
	runtimeInSet := make(map[string]struct{}, len(runtimeIDs))
	for _, rid := range runtimeIDs {
		key := util.UUIDToString(rid)
		runtimeInSet[key] = struct{}{}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		uniqueIDs = append(uniqueIDs, rid)
	}

	claimed := make([]db.AgentTaskQueue, 0, maxTasks)

	// 1. Promote due deferred tasks across the whole set (promote-first, like
	// the singular path). Replay the per-row side effects the singular service
	// method PromoteDueDeferredTasksForRuntime performs — crucially
	// EmptyClaim.Bump (via NotifyTaskEnqueued → notifyTaskAvailable) so a
	// just-promoted deferred task invalidates its runtime's cached empty
	// verdict BEFORE the empty-cache filter in step 3; otherwise a stale
	// MarkEmpty from a prior idle poll would short-circuit the runtime and the
	// promoted task would sit unclaimed until the empty key's TTL. Also emits
	// the deferred→queued UI event and the enqueue analytics sample.
	s.cancelSupersededDeferredRetries(ctx, uniqueIDs)
	promoted, err := s.Queries.PromoteDueDeferredTasksForRuntimes(ctx, db.PromoteDueDeferredTasksForRuntimesParams{
		RuntimeIds:       uniqueIDs,
		RuntimeStaleSecs: RuntimeClaimFreshnessSeconds,
	})
	if isDuplicatePendingTaskErr(err) {
		// Same tolerance as the single-runtime path, and it matters more here:
		// one contended row would otherwise fail the claim for EVERY runtime in
		// the batch. Promote nothing this tick and let the claim continue.
		slog.Info("promote deferred tasks (batch): slot taken by a concurrent enqueue, skipping this tick")
		promoted = nil
	} else if err != nil {
		return nil, fmt.Errorf("promote deferred tasks: %w", err)
	}
	for _, task := range promoted {
		slog.Info("deferred task promoted (batch)",
			"task_id", util.UUIDToString(task.ID),
			"runtime_id", util.UUIDToString(task.RuntimeID),
			"agent_id", util.UUIDToString(task.AgentID),
		)
		s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
		s.NotifyTaskEnqueued(ctx, task)
	}

	// 2. Reclaim lost-response dispatched tasks when any runtime's task schedule
	// or bounded DB backstop is due. The query always receives the complete
	// machine-level set so per-runtime backstops stay aligned and fixed UPDATE
	// setup/locking cost is paid at a predictable cadence. A nil/missing/failed
	// cache preserves the historical query path.
	runtimeKeys := make([]string, 0, len(uniqueIDs))
	for _, rid := range uniqueIDs {
		runtimeKeys = append(runtimeKeys, util.UUIDToString(rid))
	}
	checkStarted := time.Now()
	dueKeys := s.ReclaimCheck.DueRuntimeIDs(ctx, runtimeKeys, checkStarted)
	var reclaimed []db.AgentTaskQueue
	var reclaimCheckAfter time.Time
	if len(dueKeys) > 0 {
		reclaimCheckAfter = time.Now().Add(claimResponseRecoveryWindow + ReclaimCheckHintSafetyMargin)
		reclaimed, err = s.Queries.ReclaimStaleDispatchedTasksForRuntimes(ctx, db.ReclaimStaleDispatchedTasksForRuntimesParams{
			RuntimeIds:        uniqueIDs,
			ClaimRecoverySecs: claimResponseRecoveryWindow.Seconds(),
			PrepareLeaseSecs:  prepareLeaseDuration.Seconds(),
			RuntimeStaleSecs:  RuntimeClaimFreshnessSeconds,
			MaxTasks:          int32(maxTasks),
		})
		if err != nil {
			return nil, fmt.Errorf("reclaim stale dispatched tasks: %w", err)
		}
		// The UPDATE's fixed setup/locking cost dominates array width. Query and
		// advance the complete machine-level set together so per-runtime backstops
		// cannot drift into one UPDATE on almost every daemon poll.
		s.ReclaimCheck.MarkChecked(
			ctx,
			runtimeKeys,
			checkStarted,
			time.Now().Add(ReclaimCheckRetryInterval),
		)
	}
	for i := range reclaimed {
		s.trackTaskForReclaim(reclaimed[i], reclaimCheckAfter)
		claimed = append(claimed, reclaimed[i])
		slog.Info("stale dispatched task reclaimed (batch)",
			"task_id", util.UUIDToString(reclaimed[i].ID),
			"runtime_id", util.UUIDToString(reclaimed[i].RuntimeID),
			"agent_id", util.UUIDToString(reclaimed[i].AgentID),
		)
	}
	if len(claimed) >= maxTasks {
		return claimed[:maxTasks], nil
	}

	// 3. Empty-cache short-circuit + version sampling for the remaining runtimes.
	nonEmpty := make([]pgtype.UUID, 0, len(uniqueIDs))
	versions := make(map[string]int64, len(uniqueIDs))
	for _, rid := range uniqueIDs {
		key := util.UUIDToString(rid)
		if s.EmptyClaim.IsEmpty(ctx, key) {
			continue
		}
		versions[key] = s.EmptyClaim.CurrentVersion(ctx, key)
		nonEmpty = append(nonEmpty, rid)
	}
	if len(nonEmpty) == 0 {
		return claimed, nil
	}

	// 4. One candidate SELECT across the non-empty set.
	candidates, err := s.Queries.ListQueuedClaimCandidatesByRuntimes(ctx, nonEmpty)
	if err != nil {
		// Steps 2/6 commit reclaimed/claimed tasks in their own transactions,
		// so `claimed` may already hold tasks dispatched server-side. Dropping
		// them with a 500 makes the daemon HTTP-fall-back and claim a SECOND
		// batch into the same free slots (the first batch then waits for stale
		// reclaim) — the same double-claim this PR set out to remove
		// (MUL-4257). Prefer partial success: hand back what committed so the
		// handler finalizes and returns it; the errored candidates stay queued
		// for the next poll.
		if len(claimed) > 0 {
			slog.Error("batch claim: candidate query failed after partial success; returning claimed tasks to avoid loss",
				"error", err, "claimed", len(claimed))
			return claimed, nil
		}
		return nil, fmt.Errorf("list queued claim candidates: %w", err)
	}

	// 5. Mark runtimes with zero candidates empty so their next idle poll skips
	// Postgres. Runtimes that had at least one candidate are intentionally not
	// marked (positive results always re-check the DB, matching the singular
	// path).
	withCandidates := make(map[string]struct{}, len(candidates))
	for i := range candidates {
		withCandidates[util.UUIDToString(candidates[i].RuntimeID)] = struct{}{}
	}
	for _, rid := range nonEmpty {
		key := util.UUIDToString(rid)
		if _, ok := withCandidates[key]; !ok {
			s.EmptyClaim.MarkEmpty(ctx, key, versions[key])
		}
	}

	// 6. Give each agent a turn before filling its remaining capacity.
	// A successful agent rejoins the tail; a full or locked agent drops out.
	triedAgents := make(map[string]struct{}, len(candidates))
	agentCandidates := make([]db.AgentTaskQueue, 0, len(candidates))
	var claimErr error
	for i := range candidates {
		agentKey := util.UUIDToString(candidates[i].AgentID)
		if _, tried := triedAgents[agentKey]; tried {
			continue
		}
		triedAgents[agentKey] = struct{}{}
		agentCandidates = append(agentCandidates, candidates[i])
	}
	for i := 0; i < len(agentCandidates) && len(claimed) < maxTasks; i++ {
		candidate := agentCandidates[i]
		task, err := s.claimTask(ctx, candidate.AgentID, candidate.RuntimeID)
		if err != nil {
			if claimErr == nil {
				claimErr = fmt.Errorf("claim task: %w", err)
			}
			slog.Warn("batch claim: agent claim failed; continuing independent agents",
				"error", err, "agent_id", util.UUIDToString(candidate.AgentID), "claimed", len(claimed))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if task == nil {
			continue
		}
		// The SQL claim is scoped to the candidate runtime. Retain this guard as
		// a defensive contract check so a future query change cannot route work
		// to a runtime this daemon does not host.
		if _, ok := runtimeInSet[util.UUIDToString(task.RuntimeID)]; !ok {
			continue
		}
		claimed = append(claimed, *task)
		agentCandidates = append(agentCandidates, candidate)
	}

	// Never discard claims already committed by earlier agents or reclamation.
	if len(claimed) == 0 && claimErr != nil {
		return nil, claimErr
	}
	return claimed, nil
}
