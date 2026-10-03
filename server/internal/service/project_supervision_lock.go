package service

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// A nonblocking project fence prevents lock inversion with existing queue
// paths, which acquire an agent before inspecting its project admission.
func supervisionLock(ctx context.Context, q *db.Queries, id, workspaceID pgtype.UUID) error {
	if _, err := q.TryLockSupervisionWorkspace(ctx, workspaceID); err != nil {
		return ErrProjectSupervisionForbidden
	}
	locked, err := q.TryProjectSupervisionLock(ctx, util.UUIDToString(id))
	if err != nil {
		return err
	}
	if !locked {
		return ErrProjectSupervisionConflict
	}
	return nil
}
