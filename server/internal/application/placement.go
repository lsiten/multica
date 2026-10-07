package application

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func lockOperationRuntimes(ctx context.Context, q *db.Queries, workspaceID, rootID, rootRuntimeID pgtype.UUID, placements map[string]string) error {
	runtimes := map[string]pgtype.UUID{util.UUIDToString(rootRuntimeID): rootRuntimeID}
	for _, raw := range placements {
		id, err := util.ParseUUID(raw)
		if err != nil {
			return fmt.Errorf("%w: placement must select a runtime UUID", ErrInvalid)
		}
		runtimes[util.UUIDToString(id)] = id
	}
	previous, err := q.ListApplicationRootInstances(ctx, db.ListApplicationRootInstancesParams{WorkspaceID: workspaceID, RootApplicationID: rootID, RootRuntimeID: rootRuntimeID})
	if err != nil {
		return err
	}
	for _, instance := range previous {
		runtimes[util.UUIDToString(instance.RuntimeID)] = instance.RuntimeID
	}
	ids := make([]string, 0, len(runtimes))
	for id := range runtimes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if _, err := q.LockApplicationRuntime(ctx, db.LockApplicationRuntimeParams{ID: runtimes[id], WorkspaceID: workspaceID}); err != nil {
			return fmt.Errorf("lock selected application runtime: %w", err)
		}
	}
	return nil
}
