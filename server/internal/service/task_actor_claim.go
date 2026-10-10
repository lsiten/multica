package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ErrTaskActorClaim means an actor credential no longer authorizes this run.
var ErrTaskActorClaim = errors.New("task actor claim is stale or unavailable")

func taskActorLookupError(err error) error {
	var postgres *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || errors.As(err, &postgres) && postgres.Code == "55P03" {
		return ErrTaskActorClaim
	}
	return fmt.Errorf("check task actor claim: %w", err)
}

// lockTaskActorClaim runs only after the caller holds the source-task row lock.
// Runtime/agent/member and token locks are NOWAIT: finalization locks runtime and
// agent before task, so waiting here could invert that order. Contention denies
// this attempt; it never skips validation. All locks last through business commit.
func lockTaskActorClaim(ctx context.Context, q *db.Queries, current db.AgentTaskQueue, workspaceID pgtype.UUID) error {
	if auth.TrustedInternalTaskActor(ctx) {
		return nil
	}
	actor, ok := auth.TaskActorFromContext(ctx)
	if !ok || auth.IsTemporarilyDisabledUser(actor.UserID, "") {
		return ErrTaskActorClaim
	}
	token, err := q.LockTaskActorToken(ctx, actor.TokenHash)
	if err != nil {
		return taskActorLookupError(err)
	}
	if !token.ExpiresAt.Time.After(time.Now()) || util.UUIDToString(token.ID) != actor.TokenID || util.UUIDToString(token.TaskID) != actor.TaskID || token.TaskID != current.ID || token.AgentID != current.AgentID || util.UUIDToString(token.AgentID) != actor.AgentID || token.WorkspaceID != workspaceID || util.UUIDToString(token.WorkspaceID) != actor.WorkspaceID || util.UUIDToString(token.UserID) != actor.UserID {
		return ErrTaskActorClaim
	}
	binding, err := q.GetTaskActorClaim(ctx, actor.TokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		if actor.Bound {
			return ErrTaskActorClaim
		}
		fenced, err := q.HasTaskActorClaim(ctx, current.ID)
		if err != nil {
			return err
		}
		if fenced {
			return ErrTaskActorClaim
		}
		if _, err = q.GetTaskExecution(ctx, current.ID); err == nil {
			return ErrTaskActorClaim
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		_, err = q.LockLegacyTaskActorMember(ctx, db.LockLegacyTaskActorMemberParams{UserID: token.UserID, WorkspaceID: workspaceID})
		if err != nil {
			return taskActorLookupError(err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !actor.Bound || binding.TokenID != token.ID || binding.TaskID != current.ID || binding.AgentID != token.AgentID || binding.WorkspaceID != workspaceID || binding.UserID != token.UserID || binding.RuntimeID != current.RuntimeID || !current.DispatchedAt.Valid || !binding.DispatchedAt.Time.Equal(current.DispatchedAt.Time) || util.UUIDToString(binding.RuntimeID) != actor.RuntimeID || !binding.DispatchedAt.Time.Equal(actor.DispatchedAt) {
		return ErrTaskActorClaim
	}
	runtime, err := q.LockTaskActorPrincipals(ctx, db.LockTaskActorPrincipalsParams{RuntimeID: binding.RuntimeID, AgentID: binding.AgentID, WorkspaceID: binding.WorkspaceID, UserID: binding.UserID})
	if err != nil {
		return taskActorLookupError(err)
	}
	execution, err := q.GetTaskExecution(ctx, current.ID)
	if err == nil {
		if execution.Revoked || !runtime.DaemonID.Valid || execution.DaemonID != runtime.DaemonID.String || execution.RuntimeID != binding.RuntimeID || execution.WorkspaceID != workspaceID || !execution.DispatchedAt.Time.Equal(binding.DispatchedAt.Time) {
			return ErrTaskActorClaim
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return nil
}
