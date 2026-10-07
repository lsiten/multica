package application

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Cancel fences unfinished commands and retains completed or shared service instances.
func (s *Service) Cancel(ctx context.Context, workspaceID, applicationID, operationID pgtype.UUID, actor Actor) (OperationView, error) {
	if err := validateActor(actor); err != nil {
		return OperationView{}, err
	}
	app, err := s.Get(ctx, workspaceID, applicationID)
	if err != nil {
		return OperationView{}, err
	}
	projectID, err := util.ParseUUID(app.ProjectID)
	if err != nil {
		return OperationView{}, err
	}
	tx, err := s.Transactions.Begin(ctx)
	if err != nil {
		return OperationView{}, err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	if err := lockCatalogProject(ctx, q, workspaceID, projectID); err != nil {
		return OperationView{}, err
	}
	operation, err := q.LockApplicationOperation(ctx, db.LockApplicationOperationParams{ID: operationID, WorkspaceID: workspaceID})
	if err != nil {
		return OperationView{}, err
	}
	if operation.ApplicationID != applicationID {
		return OperationView{}, ErrNotFound
	}
	if operation.CancelRequestedAt.Valid {
		return operationView(ctx, q, operation)
	}
	if operation.State != "queued" && operation.State != "running" {
		return OperationView{}, fmt.Errorf("%w: operation already finished", ErrConflict)
	}
	var snapshot ExecutionSnapshot
	if err := json.Unmarshal(operation.Plan, &snapshot); err != nil {
		return OperationView{}, err
	}
	rootRuntimeID, err := util.ParseUUID(snapshot.RootRuntimeID)
	if err != nil {
		return OperationView{}, err
	}
	if err := lockOperationRuntimes(ctx, q, workspaceID, applicationID, rootRuntimeID, snapshot.Placements); err != nil {
		return OperationView{}, err
	}
	steps, err := q.ListApplicationOperationSteps(ctx, db.ListApplicationOperationStepsParams{OperationID: operationID, WorkspaceID: workspaceID})
	if err != nil {
		return OperationView{}, err
	}
	for _, step := range steps {
		if step.State == "queued" || step.State == "running" {
			if _, err := operationRuntime(ctx, q, workspaceID, step.RuntimeID, actor); err != nil {
				return OperationView{}, err
			}
		}
	}
	operation, err = q.RequestApplicationOperationCancellation(ctx, db.RequestApplicationOperationCancellationParams{ID: operationID, WorkspaceID: workspaceID, CancelActorType: pgtype.Text{String: actor.Type, Valid: true}, CancelActorID: actor.ID, CancelUserID: actor.UserID, CancelTaskID: actor.TaskID})
	if err != nil {
		return OperationView{}, err
	}
	if err := q.CancelApplicationOperationSteps(ctx, db.CancelApplicationOperationStepsParams{OperationID: operationID, WorkspaceID: workspaceID}); err != nil {
		return OperationView{}, err
	}
	for _, step := range steps {
		if step.State != "queued" && step.State != "running" {
			continue
		}
		if err := cancelApplicationStep(ctx, q, step, applicationID, rootRuntimeID); err != nil {
			return OperationView{}, err
		}
		if step.Action == "start" || step.Action == "restart" {
			var command protocol.ApplicationControlCommand
			if err := json.Unmarshal(step.Command, &command); err != nil {
				return OperationView{}, err
			}
			command.ConsumerCreated = false
			if err := releaseFailedConsumers(ctx, q, step, command); err != nil {
				return OperationView{}, err
			}
		}
	}
	if err := refreshOperationState(ctx, q, operation); err != nil {
		return OperationView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OperationView{}, err
	}
	return s.GetOperation(ctx, workspaceID, operationID)
}

func cancelApplicationStep(ctx context.Context, q *db.Queries, step db.ApplicationOperationStep, rootID, rootRuntimeID pgtype.UUID) error {
	var command protocol.ApplicationControlCommand
	if err := json.Unmarshal(step.Command, &command); err != nil {
		return err
	}
	if step.Action == "publish" || step.Action == "unpublish" {
		return nil
	}
	instance, err := q.GetApplicationInstance(ctx, db.GetApplicationInstanceParams{ID: step.InstanceID, WorkspaceID: step.WorkspaceID})
	if err != nil {
		return err
	}
	if instance.Generation != step.Generation {
		return nil
	}
	if step.Action == "start" || step.Action == "restart" {
		if instance.ProcessState == "running" && instance.ObservedGeneration == instance.Generation {
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
		instance, err = q.SetApplicationInstanceDesiredState(ctx, db.SetApplicationInstanceDesiredStateParams{ID: instance.ID, WorkspaceID: step.WorkspaceID, DesiredState: "stopped"})
		if err != nil {
			return err
		}
		if !step.ClaimedAt.Valid && instance.ProcessState == "stopped" && instance.ObservedGeneration == step.Generation-1 {
			_, err := q.ConfirmUnstartedApplicationCancellation(ctx, db.ConfirmUnstartedApplicationCancellationParams{ID: instance.ID, WorkspaceID: step.WorkspaceID, Generation: instance.Generation})
			return err
		}
		command.Action = "stop"
	} else if step.Action == "stop" {
		if !step.ClaimedAt.Valid {
			raw, err := q.GetApplicationInstanceStartCommand(ctx, db.GetApplicationInstanceStartCommandParams{InstanceID: instance.ID, WorkspaceID: step.WorkspaceID})
			if err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &command); err != nil {
				return err
			}
			instance, err = q.SetApplicationInstanceDesiredState(ctx, db.SetApplicationInstanceDesiredStateParams{ID: instance.ID, WorkspaceID: step.WorkspaceID, DesiredState: "running"})
			if err != nil {
				return err
			}
			command.Action = "resume"
		} else {
			instance, err = q.SetApplicationInstanceDesiredState(ctx, db.SetApplicationInstanceDesiredStateParams{ID: instance.ID, WorkspaceID: step.WorkspaceID, DesiredState: "stopped"})
			if err != nil {
				return err
			}
			command.Action = "stop"
		}
	} else {
		return nil
	}
	command.Generation = instance.Generation
	command.RootApplicationID = util.UUIDToString(rootID)
	command.RootRuntimeID = util.UUIDToString(rootRuntimeID)
	command.Dependencies = []protocol.ApplicationInstanceDependency{}
	command.ConsumerCreated = false
	raw, err := json.Marshal(command)
	if err != nil {
		return err
	}
	_, err = q.InsertApplicationOperationStep(ctx, db.InsertApplicationOperationStepParams{WorkspaceID: step.WorkspaceID, OperationID: step.OperationID, InstanceID: step.InstanceID, ApplicationID: step.ApplicationID, RuntimeID: step.RuntimeID, Generation: instance.Generation, Wave: 0, Required: true, Action: command.Action, Command: raw})
	return err
}
