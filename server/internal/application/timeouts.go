package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const operationTimeoutMessage = "application operation timed out; unconfirmed processes are reconciled when the runtime reconnects"

func operationDeadline(createdAt time.Time, steps []db.ApplicationOperationStep) (time.Time, error) {
	budget := 3 * time.Hour
	for _, step := range steps {
		var command protocol.ApplicationControlCommand
		if err := json.Unmarshal(step.Command, &command); err != nil {
			return time.Time{}, err
		}
		budget += 10 * time.Minute
		for _, preparation := range command.Config.Prepare {
			budget += time.Duration(preparation.TimeoutSeconds) * time.Second
		}
		attempts := 0
		if command.Config.Restart.Enabled {
			attempts = command.Config.Restart.MaxAttempts
		}
		if command.Config.Health.Kind != "none" {
			budget += time.Duration(command.Config.Health.TimeoutSeconds*(1+attempts)) * time.Second
		}
		budget += time.Duration(command.Config.Restart.DelaySeconds*attempts) * time.Second
	}
	return createdAt.Add(budget), nil
}

// ExpireOperations bounds unfinished work without claiming that an unreachable process exited.
func (s *Service) ExpireOperations(ctx context.Context, now time.Time, batchSize int32) ([]OperationView, error) {
	if batchSize < 1 || batchSize > 64 {
		return nil, fmt.Errorf("%w: expiration batch must contain 1 to 64 operations", ErrInvalid)
	}
	operations, err := s.Queries.ListExpiredApplicationOperations(ctx, db.ListExpiredApplicationOperationsParams{ExpiredAt: pgtype.Timestamptz{Time: now, Valid: true}, BatchSize: batchSize})
	if err != nil {
		return nil, err
	}
	changed := []OperationView{}
	var failures error
	for _, operation := range operations {
		operationCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		view, expired, err := s.expireOperation(operationCtx, operation, now)
		cancel()
		if err != nil {
			failures = errors.Join(failures, err)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if expired {
			changed = append(changed, view)
		}
	}
	return changed, failures
}

func (s *Service) expireOperation(ctx context.Context, candidate db.ApplicationOperation, now time.Time) (OperationView, bool, error) {
	app, err := s.Queries.GetApplication(ctx, db.GetApplicationParams{ID: candidate.ApplicationID, WorkspaceID: candidate.WorkspaceID})
	if err != nil {
		return OperationView{}, false, err
	}
	tx, err := s.Transactions.Begin(ctx)
	if err != nil {
		return OperationView{}, false, err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	if err := lockCatalogProject(ctx, q, candidate.WorkspaceID, app.ProjectID); err != nil {
		return OperationView{}, false, err
	}
	operation, err := q.LockApplicationOperation(ctx, db.LockApplicationOperationParams{ID: candidate.ID, WorkspaceID: candidate.WorkspaceID})
	if err != nil {
		return OperationView{}, false, err
	}
	if operation.State != "queued" && operation.State != "running" && operation.State != "cancelling" || operation.DeadlineAt.Time.After(now) {
		return OperationView{}, false, nil
	}
	var snapshot ExecutionSnapshot
	if err := json.Unmarshal(operation.Plan, &snapshot); err != nil {
		return OperationView{}, false, err
	}
	rootRuntimeID, err := util.ParseUUID(snapshot.RootRuntimeID)
	if err != nil {
		return OperationView{}, false, err
	}
	if err := lockOperationRuntimes(ctx, q, operation.WorkspaceID, operation.ApplicationID, rootRuntimeID, snapshot.Placements); err != nil {
		return OperationView{}, false, err
	}
	steps, err := q.ListApplicationOperationSteps(ctx, db.ListApplicationOperationStepsParams{OperationID: operation.ID, WorkspaceID: operation.WorkspaceID})
	if err != nil {
		return OperationView{}, false, err
	}
	if err := q.FailPendingApplicationOperationSteps(ctx, db.FailPendingApplicationOperationStepsParams{OperationID: operation.ID, WorkspaceID: operation.WorkspaceID, Error: operationTimeoutMessage}); err != nil {
		return OperationView{}, false, err
	}
	for _, step := range steps {
		if step.State != "queued" && step.State != "running" {
			continue
		}
		if err := expireApplicationStep(ctx, q, step, operation.ApplicationID, rootRuntimeID); err != nil {
			return OperationView{}, false, err
		}
	}
	if err := refreshOperationState(ctx, q, operation); err != nil {
		return OperationView{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OperationView{}, false, err
	}
	view, err := s.GetOperation(ctx, operation.WorkspaceID, operation.ID)
	return view, true, err
}

func expireApplicationStep(ctx context.Context, q *db.Queries, step db.ApplicationOperationStep, rootID, rootRuntimeID pgtype.UUID) error {
	if step.Action != "start" && step.Action != "restart" && step.Action != "stop" {
		return nil
	}
	instance, err := q.GetApplicationInstance(ctx, db.GetApplicationInstanceParams{ID: step.InstanceID, WorkspaceID: step.WorkspaceID})
	if err != nil || instance.Generation != step.Generation {
		return err
	}
	if step.Action == "stop" && instance.DesiredState == "stopped" && instance.ProcessState == "stopped" && instance.ObservedGeneration == instance.Generation {
		return nil
	}
	var command protocol.ApplicationControlCommand
	if err := json.Unmarshal(step.Command, &command); err != nil {
		return err
	}
	if step.Action != "stop" {
		ready := instance.ProcessState == "running" && instance.ObservedGeneration == instance.Generation && instance.ObservedRevision == instance.Revision && (command.Config.Health.Kind == "none" || instance.HealthState == "healthy")
		if ready {
			return nil
		}
		if command.ConsumerCreated {
			if err := q.ReleaseNewApplicationConsumer(ctx, db.ReleaseNewApplicationConsumerParams{WorkspaceID: step.WorkspaceID, InstanceID: step.InstanceID, CreatedOperationID: step.OperationID}); err != nil {
				return err
			}
		}
		consumers, err := q.ListApplicationInstanceConsumers(ctx, db.ListApplicationInstanceConsumersParams{WorkspaceID: step.WorkspaceID, InstanceID: step.InstanceID})
		if err != nil {
			return err
		}
		for _, consumer := range consumers {
			if consumer.RootApplicationID != rootID || consumer.RootRuntimeID != rootRuntimeID {
				return nil
			}
		}
	}
	previous := instance
	instance, err = q.SetApplicationInstanceDesiredState(ctx, db.SetApplicationInstanceDesiredStateParams{ID: instance.ID, WorkspaceID: instance.WorkspaceID, DesiredState: "stopped"})
	if err != nil {
		return err
	}
	if !step.ClaimedAt.Valid && previous.ProcessState == "stopped" && previous.ObservedGeneration == step.Generation-1 {
		if _, err := q.ConfirmUnstartedApplicationCancellation(ctx, db.ConfirmUnstartedApplicationCancellationParams{ID: instance.ID, WorkspaceID: instance.WorkspaceID, Generation: instance.Generation}); err != nil {
			return err
		}
	}
	command.ConsumerCreated = false
	return releaseFailedConsumers(ctx, q, step, command)
}
