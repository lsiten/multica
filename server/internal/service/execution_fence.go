package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ErrExecutionStale distinguishes a rejected callback from infrastructure failures.
var ErrExecutionStale = errors.New("task execution is stale or unauthorized")

// LockExecutionCallback must run in the same transaction as the business write,
// after the chat-session lock. Internal business operations without a callback
// context keep their existing authority (cancellation, expiry, and recovery).
func LockExecutionCallback(ctx context.Context, q *db.Queries, taskID pgtype.UUID) error {
	request := auth.ExecutionRequestFromContext(ctx)
	if !request.Present {
		return nil
	}
	task, err := q.LockTaskForExecution(ctx, taskID)
	if err != nil {
		return fmt.Errorf("lock execution task: %w", err)
	}
	execution, err := q.GetTaskExecution(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		if request.ExecutionID != "" || request.GrantHash != "" {
			return ErrExecutionStale
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read execution: %w", err)
	}
	// A task that has ever opted into execution authority never downgrades to
	// identity-free writes, even while its next claim is waiting to bind.
	if execution.Revoked || request.ExecutionID != util.UUIDToString(execution.ExecutionID) || request.WorkerID != util.UUIDToString(execution.WorkerID) || task.RuntimeID != execution.RuntimeID || !task.DispatchedAt.Valid || !task.DispatchedAt.Time.Equal(execution.DispatchedAt.Time) {
		return ErrExecutionStale
	}
	_, err = q.RuntimeExecutionMembership(ctx, db.RuntimeExecutionMembershipParams{RuntimeID: execution.RuntimeID, WorkspaceID: execution.WorkspaceID, DaemonID: pgtype.Text{String: execution.DaemonID, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrExecutionStale
	}
	if err != nil {
		return fmt.Errorf("execution membership: %w", err)
	}
	if request.GrantHash != "" {
		grant, err := q.GetExecutionGrant(ctx, request.GrantHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrExecutionStale
		}
		if err != nil {
			return fmt.Errorf("execution grant: %w", err)
		}
		if !grant.ExpiresAt.Time.After(time.Now()) || grant.TaskID != taskID || grant.ExecutionID != execution.ExecutionID || !slices.Contains(grant.Operations, request.Operation) {
			return ErrExecutionStale
		}
	} else {
		return ErrExecutionStale
	}
	if task.CompletedAt.Valid && time.Since(task.CompletedAt.Time) > 24*time.Hour {
		return ErrExecutionStale
	}
	switch request.Operation {
	case "status", "usage", "messages", "jev-decision-logs", "session", "cancel-ack", "supplements/ack", "worktree-delivery", "project-graph/events":
		return nil
	case "complete":
		// The HTTP operation stays complete for grant authorization, even
		// when its payload was normalized into a specific failed outcome.
		normalizedReplay := task.Status == "failed" && request.NormalizedFailureReason != "" && task.FailureReason.String == request.NormalizedFailureReason
		if task.Status == "completed" || task.Status == "running" || normalizedReplay {
			return nil
		}
	case "fail":
		if task.Status == "failed" || task.Status == "running" || task.Status == "dispatched" || task.Status == "waiting_local_directory" {
			return nil
		}
	default:
		if task.Status == "running" || task.Status == "dispatched" || task.Status == "waiting_local_directory" {
			return nil
		}
	}
	return ErrExecutionStale
}
