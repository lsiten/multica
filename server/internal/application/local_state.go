package application

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (s *Service) ApplyLocalDesiredState(ctx context.Context, workspaceID, runtimeID pgtype.UUID, observation protocol.ApplicationObservation, desired string) (bool, error) {
	if desired != "running" && desired != "stopped" {
		return false, ErrInvalid
	}
	id, err := util.ParseUUID(observation.InstanceID)
	if err != nil {
		return false, ErrInvalid
	}
	current, err := s.Queries.GetApplicationInstance(ctx, db.GetApplicationInstanceParams{ID: id, WorkspaceID: workspaceID})
	if err != nil {
		return false, err
	}
	if current.RuntimeID != runtimeID {
		return false, ErrForbidden
	}
	pending, err := s.Queries.CountApplicationInstanceOperations(ctx, db.CountApplicationInstanceOperationsParams{InstanceID: id, WorkspaceID: workspaceID})
	if err != nil {
		return false, err
	}
	if pending > 0 {
		return false, ErrConflict
	}
	if current.Generation != observation.Generation || current.Revision != observation.Revision {
		return false, ErrConflict
	}
	if current.DesiredState == desired {
		return false, nil
	}
	_, err = s.Queries.ApplyLocalApplicationDesiredState(ctx, db.ApplyLocalApplicationDesiredStateParams{ID: id, WorkspaceID: workspaceID, DesiredState: desired, Generation: observation.Generation, Revision: observation.Revision})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrConflict
	}
	return err == nil, err
}
