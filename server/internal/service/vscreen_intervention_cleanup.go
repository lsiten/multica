package service

import (
	"context"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RetireDisabledVscreenInterventions is invoked only after the command broker has
// verified native disposal. Runtime/member locks fence concurrent continuation,
// report creation and ownership changes; sibling runtimes remain untouched.
func (s *TaskService) RetireDisabledVscreenInterventions(ctx context.Context, expected db.AgentRuntime, daemonID string) error {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	if _, err = q.LockVscreenWorkspace(ctx, expected.WorkspaceID); err != nil {
		return err
	}
	if _, err = q.LockActiveMember(ctx, db.LockActiveMemberParams{UserID: expected.OwnerID, WorkspaceID: expected.WorkspaceID}); err != nil {
		return InterventionError("permission_denied")
	}
	rt, err := q.LockAgentRuntime(ctx, expected.ID)
	if err != nil {
		return err
	}
	if rt.WorkspaceID != expected.WorkspaceID || rt.OwnerID != expected.OwnerID || !rt.DaemonID.Valid || rt.DaemonID.String != daemonID {
		return InterventionError("permission_denied")
	}
	if err = q.CancelVscreenInterventionsByRuntime(ctx, rt.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
