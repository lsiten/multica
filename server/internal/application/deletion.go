package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// DeletionConflict preserves the affected definitions without exposing private runtime details.
type DeletionConflict struct {
	Blockers []db.ListApplicationDeletionBlockersRow
}

func (conflict *DeletionConflict) Error() string {
	names := make([]string, 0, len(conflict.Blockers))
	for _, app := range conflict.Blockers {
		names = append(names, app.Name)
	}
	return "stop applications and wait for runtime confirmation before deleting: " + strings.Join(names, ", ")
}

func (conflict *DeletionConflict) Unwrap() error { return ErrConflict }

// CheckDeletion runs after the caller locks the workspace, project or runtime being deleted.
func CheckDeletion(ctx context.Context, q *db.Queries, scope db.ListApplicationDeletionBlockersParams) error {
	rows, err := q.ListApplicationDeletionBlockers(ctx, scope)
	if err != nil {
		return fmt.Errorf("check application ownership before deletion: %w", err)
	}
	if len(rows) > 0 {
		return &DeletionConflict{Blockers: rows}
	}
	return nil
}

func lockCatalogProject(ctx context.Context, q *db.Queries, workspaceID, projectID pgtype.UUID) error {
	if _, err := q.LockApplicationWorkspace(ctx, workspaceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if _, err := q.LockApplicationProject(ctx, db.LockApplicationProjectParams{ID: projectID, WorkspaceID: workspaceID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// CleanupCatalog removes stopped application data inside the parent's fenced transaction.
func CleanupCatalog(ctx context.Context, q *db.Queries, workspaceID, projectID pgtype.UUID) error {
	params := db.DeleteScopedApplicationTicketsParams{WorkspaceID: workspaceID, ProjectID: projectID}
	steps := []func() error{
		func() error { return q.DeleteScopedApplicationTickets(ctx, params) },
		func() error {
			return q.DeleteScopedApplicationConsumers(ctx, db.DeleteScopedApplicationConsumersParams(params))
		},
		func() error {
			return q.DeleteScopedApplicationEndpoints(ctx, db.DeleteScopedApplicationEndpointsParams(params))
		},
		func() error {
			return q.DeleteScopedApplicationSteps(ctx, db.DeleteScopedApplicationStepsParams(params))
		},
		func() error {
			return q.DeleteScopedApplicationOperations(ctx, db.DeleteScopedApplicationOperationsParams(params))
		},
		func() error {
			return q.DeleteScopedApplicationInstances(ctx, db.DeleteScopedApplicationInstancesParams(params))
		},
		func() error {
			return q.DeleteScopedApplicationRelations(ctx, db.DeleteScopedApplicationRelationsParams(params))
		},
		func() error {
			return q.DeleteScopedApplicationRevisions(ctx, db.DeleteScopedApplicationRevisionsParams(params))
		},
		func() error { return q.DeleteScopedApplications(ctx, db.DeleteScopedApplicationsParams(params)) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("clean application catalog: %w", err)
		}
	}
	return nil
}

// CleanupRuntime preserves definitions and operation history when removing stopped instances.
func CleanupRuntime(ctx context.Context, q *db.Queries, workspaceID, runtimeID pgtype.UUID) error {
	params := db.DeleteRuntimeApplicationTicketsParams{WorkspaceID: workspaceID, RuntimeID: runtimeID}
	steps := []func() error{
		func() error { return q.DeleteRuntimeApplicationTickets(ctx, params) },
		func() error {
			return q.DeleteRuntimeApplicationConsumers(ctx, db.DeleteRuntimeApplicationConsumersParams(params))
		},
		func() error {
			return q.DeleteRuntimeApplicationEndpoints(ctx, db.DeleteRuntimeApplicationEndpointsParams(params))
		},
		func() error {
			return q.DeleteRuntimeApplicationInstances(ctx, db.DeleteRuntimeApplicationInstancesParams(params))
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("clean runtime application instances: %w", err)
		}
	}
	return nil
}
