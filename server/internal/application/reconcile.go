package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func dependencyStatus(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, dependency protocol.ApplicationInstanceDependency) (bool, bool, error) {
	id, err := util.ParseUUID(dependency.InstanceID)
	if err != nil {
		return false, true, err
	}
	instance, err := q.GetApplicationInstance(ctx, db.GetApplicationInstanceParams{ID: id, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, true, nil
	}
	if err != nil {
		return false, false, err
	}
	if instance.Generation != dependency.Generation || instance.DesiredState != "running" || instance.ProcessState == "failed" || instance.ProcessState == "stopped" {
		return false, true, nil
	}
	runtime, err := q.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{ID: instance.RuntimeID, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, true, nil
	}
	if err != nil {
		return false, false, err
	}
	fresh := instance.ObservedAt.Valid && time.Since(instance.ObservedAt.Time) < 45*time.Second && runtime.Status == "online" && runtime.LastSeenAt.Valid && time.Since(runtime.LastSeenAt.Time) < 3*time.Minute
	ready := fresh && instance.ProcessState == "running" && instance.ObservedRevision == instance.Revision && instance.ObservedGeneration == instance.Generation
	if dependency.Condition == "healthy" {
		ready = ready && instance.HealthState == "healthy"
	}
	return ready, false, nil
}

// Claim returns only commands belonging to this exact runtime after checking prerequisites.
func (s *Service) Claim(ctx context.Context, workspaceID, runtimeID pgtype.UUID) ([]protocol.ApplicationClaim, error) {
	tx, err := s.Transactions.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	if _, err = q.LockApplicationWorkspace(ctx, workspaceID); err != nil {
		return nil, err
	}
	projects, err := q.LockRuntimeApplicationProjects(ctx, db.LockRuntimeApplicationProjectsParams{WorkspaceID: workspaceID, RuntimeID: runtimeID})
	if err != nil {
		return nil, err
	}
	steps, err := q.ClaimApplicationOperationSteps(ctx, db.ClaimApplicationOperationStepsParams{WorkspaceID: workspaceID, RuntimeID: runtimeID, ProjectIds: projects})
	if err != nil {
		return nil, err
	}
	claims := make([]protocol.ApplicationClaim, 0, len(steps))
	operations := make(map[string]pgtype.UUID)
	for _, step := range steps {
		var command protocol.ApplicationControlCommand
		if err = json.Unmarshal(step.Command, &command); err != nil {
			return nil, err
		}
		instance, instanceErr := q.GetApplicationInstance(ctx, db.GetApplicationInstanceParams{ID: step.InstanceID, WorkspaceID: workspaceID})
		if instanceErr != nil {
			return nil, instanceErr
		}
		blocked := instance.Generation != step.Generation
		if command.Action != "stop" && command.Action != "resume" && command.Action != "unpublish" {
			authorized, err := applicationCommandAuthorized(ctx, q, command)
			if err != nil {
				return nil, err
			}
			blocked = blocked || !authorized
		}
		waiting := false
		for _, dependency := range command.Dependencies {
			ready, terminal, dependencyErr := dependencyStatus(ctx, q, workspaceID, dependency)
			if dependencyErr != nil {
				return nil, dependencyErr
			}
			if !ready {
				blocked = blocked || terminal
				waiting = true
			}
		}
		if waiting && step.ClaimedAt.Valid && time.Since(step.ClaimedAt.Time) > time.Duration(max(60, command.Config.Health.TimeoutSeconds))*time.Second {
			blocked = true
		}
		if blocked {
			if _, err = q.CompleteApplicationOperationStep(ctx, db.CompleteApplicationOperationStepParams{ID: step.ID, WorkspaceID: workspaceID, RuntimeID: runtimeID, ClaimToken: step.ClaimToken, State: "blocked", Error: "application authorization or dependency is unavailable, or the instance generation changed"}); err != nil {
				return nil, err
			}
			if err = releaseFailedConsumers(ctx, q, step, command); err != nil {
				return nil, err
			}
		} else if waiting {
			if err = q.DeferApplicationOperationStep(ctx, db.DeferApplicationOperationStepParams{ID: step.ID, WorkspaceID: workspaceID, ClaimToken: step.ClaimToken}); err != nil {
				return nil, err
			}
		} else {
			claims = append(claims, protocol.ApplicationClaim{StepID: util.UUIDToString(step.ID), OperationID: util.UUIDToString(step.OperationID), ClaimToken: util.UUIDToString(step.ClaimToken), Command: command})
		}
		operations[util.UUIDToString(step.OperationID)] = step.OperationID
	}
	for _, id := range sortedKeys(operations) {
		operation, lockErr := q.LockApplicationOperation(ctx, db.LockApplicationOperationParams{ID: operations[id], WorkspaceID: workspaceID})
		if lockErr != nil {
			return nil, lockErr
		}
		if err = refreshOperationState(ctx, q, operation); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return claims, nil
}

// RenewLease keeps a single claim alive during preparation and readiness checks.
func (s *Service) RenewLease(ctx context.Context, workspaceID, runtimeID, stepID, claimToken pgtype.UUID) error {
	step, err := s.Queries.ReadApplicationOperationStep(ctx, db.ReadApplicationOperationStepParams{ID: stepID, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (step.RuntimeID != runtimeID || step.ClaimToken != claimToken || step.State != "running") {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	var command protocol.ApplicationControlCommand
	if err := json.Unmarshal(step.Command, &command); err != nil {
		return err
	}
	if command.Action != "stop" && command.Action != "resume" && command.Action != "unpublish" {
		authorized, err := applicationCommandAuthorized(ctx, s.Queries, command)
		if err != nil {
			return err
		}
		if !authorized {
			return ErrForbidden
		}
	}
	count, err := s.Queries.ExtendApplicationOperationStepLease(ctx, db.ExtendApplicationOperationStepLeaseParams{ID: stepID, WorkspaceID: workspaceID, RuntimeID: runtimeID, ClaimToken: claimToken})
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func validObservation(observation protocol.ApplicationObservation) error {
	if observation.Generation < 1 || observation.Revision < 1 || !slices.Contains([]string{"preparing", "starting", "running", "stopping", "stopped", "failed", "unknown"}, observation.ProcessState) || !slices.Contains([]string{"checking", "healthy", "unhealthy", "none", "unknown"}, observation.HealthState) {
		return fmt.Errorf("%w: invalid application observation", ErrInvalid)
	}
	if len(observation.Error) > 2048 || len(observation.CodeVersion) > 256 || len(observation.Metrics) > 8 {
		return fmt.Errorf("%w: application observation is too large", ErrInvalid)
	}
	for name, value := range observation.Metrics {
		if (name != "rss_bytes" && name != "cpu_percent") || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("%w: invalid application metrics", ErrInvalid)
		}
	}
	return nil
}

func storeObservation(ctx context.Context, q *db.Queries, workspaceID, runtimeID pgtype.UUID, observation protocol.ApplicationObservation) error {
	if err := validObservation(observation); err != nil {
		return err
	}
	id, err := util.ParseUUID(observation.InstanceID)
	if err != nil {
		return fmt.Errorf("%w: invalid instance_id", ErrInvalid)
	}
	instance, err := q.GetApplicationInstance(ctx, db.GetApplicationInstanceParams{ID: id, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if instance.RuntimeID != runtimeID {
		return ErrForbidden
	}
	if instance.Generation != observation.Generation || instance.Revision != observation.Revision {
		return ErrConflict
	}
	var startedAt pgtype.Timestamptz
	if observation.StartedAt != nil {
		started, parseErr := time.Parse(time.RFC3339Nano, *observation.StartedAt)
		if parseErr != nil {
			return fmt.Errorf("%w: invalid started_at", ErrInvalid)
		}
		startedAt = pgtype.Timestamptz{Time: started, Valid: true}
	}
	metrics := observation.Metrics
	if metrics == nil {
		metrics = map[string]float64{}
	}
	raw, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	_, err = q.ReportApplicationInstance(ctx, db.ReportApplicationInstanceParams{ID: id, WorkspaceID: workspaceID, ObservedGeneration: observation.Generation, ObservedRevision: observation.Revision, ProcessState: observation.ProcessState, HealthState: observation.HealthState, Error: observation.Error, CodeVersion: observation.CodeVersion, Dirty: observation.Dirty, StartedAt: startedAt, Metrics: raw})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return err
}

// Observe persists local facts without treating a network outage as a process exit.
func (s *Service) Observe(ctx context.Context, workspaceID, runtimeID pgtype.UUID, observation protocol.ApplicationObservation) error {
	return storeObservation(ctx, s.Queries, workspaceID, runtimeID, observation)
}

// RuntimeInstances reconciles local processes from durable commands instead of task lifetimes.
func (s *Service) RuntimeInstances(ctx context.Context, workspaceID, runtimeID pgtype.UUID) ([]protocol.ApplicationRuntimeInstance, error) {
	instances, err := s.Queries.ListRuntimeApplicationInstances(ctx, db.ListRuntimeApplicationInstancesParams{WorkspaceID: workspaceID, RuntimeID: runtimeID})
	if err != nil {
		return nil, err
	}
	result := make([]protocol.ApplicationRuntimeInstance, 0, len(instances))
	for _, instance := range instances {
		raw, getErr := s.Queries.GetApplicationInstanceStartCommand(ctx, db.GetApplicationInstanceStartCommandParams{InstanceID: instance.ID, WorkspaceID: workspaceID})
		if errors.Is(getErr, pgx.ErrNoRows) {
			continue
		}
		if getErr != nil {
			return nil, getErr
		}
		var command protocol.ApplicationControlCommand
		if err = json.Unmarshal(raw, &command); err != nil {
			return nil, err
		}
		if command.Revision != instance.Revision {
			return nil, fmt.Errorf("application instance revision has no matching start command")
		}
		command.Generation = instance.Generation
		resume := command.Action == "resume"
		command.Action = "start"
		if instance.DesiredState == "stopped" {
			command.Action = "stop"
		} else if resume {
			command.Action = "resume"
		}
		canRestore, err := s.Queries.ApplicationInstanceMayRestore(ctx, db.ApplicationInstanceMayRestoreParams{WorkspaceID: workspaceID, InstanceID: instance.ID, Generation: instance.Generation})
		if err != nil {
			return nil, err
		}
		authorized, err := applicationCommandAuthorized(ctx, s.Queries, command)
		if err != nil {
			return nil, err
		}
		pending, err := s.Queries.CountApplicationInstanceOperations(ctx, db.CountApplicationInstanceOperationsParams{InstanceID: instance.ID, WorkspaceID: workspaceID})
		if err != nil {
			return nil, err
		}
		confirmedStopped := instance.DesiredState == "stopped" && instance.ProcessState == "stopped" && instance.Generation == instance.ObservedGeneration && pending == 0
		result = append(result, protocol.ApplicationRuntimeInstance{DesiredState: instance.DesiredState, CanRestore: canRestore.Valid && canRestore.Bool && authorized, HasPendingOperation: pending > 0, ConfirmedStopped: confirmedStopped, Command: command})
	}
	return result, nil
}

// Complete acknowledges only the exact current claim and atomically applies its side effects.
func (s *Service) Complete(ctx context.Context, workspaceID, runtimeID, stepID pgtype.UUID, result protocol.ApplicationStepResult) (pgtype.UUID, error) {
	if !slices.Contains([]string{"completed", "failed", "blocked"}, result.State) || len(result.Error) > 2048 {
		return pgtype.UUID{}, fmt.Errorf("%w: invalid step result", ErrInvalid)
	}
	claimToken, err := util.ParseUUID(result.ClaimToken)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: invalid claim_token", ErrInvalid)
	}
	tx, err := s.Transactions.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	if _, err = q.LockApplicationWorkspace(ctx, workspaceID); err != nil {
		return pgtype.UUID{}, err
	}
	scope, err := q.ReadApplicationOperationStep(ctx, db.ReadApplicationOperationStepParams{ID: stepID, WorkspaceID: workspaceID})
	if err != nil {
		return pgtype.UUID{}, err
	}
	app, err := q.GetApplication(ctx, db.GetApplicationParams{ID: scope.ApplicationID, WorkspaceID: workspaceID})
	if err != nil {
		return pgtype.UUID{}, err
	}
	if err := lockCatalogProject(ctx, q, workspaceID, app.ProjectID); err != nil {
		return pgtype.UUID{}, err
	}
	step, err := q.GetApplicationOperationStep(ctx, db.GetApplicationOperationStepParams{ID: stepID, WorkspaceID: workspaceID})
	if err != nil {
		return pgtype.UUID{}, err
	}
	if step.RuntimeID != runtimeID || step.ClaimToken != claimToken {
		return pgtype.UUID{}, ErrConflict
	}
	if step.State == result.State && step.CompletedAt.Valid {
		return step.OperationID, nil
	}
	if step.State != "running" {
		return pgtype.UUID{}, ErrConflict
	}
	if result.Observation.InstanceID != util.UUIDToString(step.InstanceID) || result.Observation.Generation != step.Generation {
		return pgtype.UUID{}, ErrConflict
	}
	var command protocol.ApplicationControlCommand
	if err = json.Unmarshal(step.Command, &command); err != nil {
		return pgtype.UUID{}, err
	}
	if command.Action != "stop" && command.Action != "resume" && command.Action != "unpublish" {
		authorized, err := applicationCommandAuthorized(ctx, q, command)
		if err != nil {
			return pgtype.UUID{}, err
		}
		if !authorized {
			return pgtype.UUID{}, ErrForbidden
		}
	}
	if result.State == "completed" {
		if command.Action == "stop" && result.Observation.ProcessState != "stopped" {
			return pgtype.UUID{}, fmt.Errorf("%w: stop did not confirm process exit", ErrInvalid)
		}
		if (command.Action == "start" || command.Action == "restart" || command.Action == "publish") && (result.Observation.ProcessState != "running" || (command.Config.Health.Kind != "none" && result.Observation.HealthState != "healthy")) {
			return pgtype.UUID{}, fmt.Errorf("%w: service is not ready", ErrInvalid)
		}
		if command.Action == "resume" && result.Observation.ProcessState != "running" {
			return pgtype.UUID{}, fmt.Errorf("%w: cancelled stop did not retain a running process", ErrInvalid)
		}
	}
	if err = storeObservation(ctx, q, workspaceID, runtimeID, result.Observation); err != nil {
		return pgtype.UUID{}, err
	}
	step, err = q.CompleteApplicationOperationStep(ctx, db.CompleteApplicationOperationStepParams{ID: stepID, WorkspaceID: workspaceID, RuntimeID: runtimeID, ClaimToken: claimToken, State: result.State, Error: result.Error})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, ErrConflict
	}
	if err != nil {
		return pgtype.UUID{}, err
	}
	if result.State == "completed" {
		rootID, parseErr := util.ParseUUID(command.RootApplicationID)
		if parseErr != nil {
			return pgtype.UUID{}, parseErr
		}
		rootRuntime, parseErr := util.ParseUUID(command.RootRuntimeID)
		if parseErr != nil {
			return pgtype.UUID{}, parseErr
		}
		if command.Action == "stop" {
			if err = q.ReleaseApplicationConsumer(ctx, db.ReleaseApplicationConsumerParams{WorkspaceID: workspaceID, InstanceID: step.InstanceID, RootApplicationID: rootID, RootRuntimeID: rootRuntime}); err != nil {
				return pgtype.UUID{}, err
			}
		}
		if command.Action == "publish" || ((command.Action == "start" || command.Action == "restart") && command.Config.AutoPublish) {
			operation, getErr := q.GetApplicationOperation(ctx, db.GetApplicationOperationParams{ID: step.OperationID, WorkspaceID: workspaceID})
			if getErr != nil {
				return pgtype.UUID{}, getErr
			}
			if _, err = q.UpsertApplicationEndpoint(ctx, db.UpsertApplicationEndpointParams{WorkspaceID: workspaceID, ApplicationID: step.ApplicationID, InstanceID: step.InstanceID, Port: int32(command.Config.Port), EntryPath: command.Config.EntryPath, Visibility: "workspace", PublishedBy: operation.UserID, State: "published"}); err != nil {
				return pgtype.UUID{}, err
			}
		}
	} else {
		if err = releaseFailedConsumers(ctx, q, step, command); err != nil {
			return pgtype.UUID{}, err
		}
	}
	operation, err := q.LockApplicationOperation(ctx, db.LockApplicationOperationParams{ID: step.OperationID, WorkspaceID: workspaceID})
	if err != nil {
		return pgtype.UUID{}, err
	}
	if err = refreshOperationState(ctx, q, operation); err != nil {
		return pgtype.UUID{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return pgtype.UUID{}, err
	}
	return step.OperationID, nil
}

func releaseFailedConsumers(ctx context.Context, q *db.Queries, step db.ApplicationOperationStep, command protocol.ApplicationControlCommand) error {
	if command.Action != "start" && command.Action != "restart" {
		return nil
	}
	if command.ConsumerCreated {
		if err := q.ReleaseNewApplicationConsumer(ctx, db.ReleaseNewApplicationConsumerParams{WorkspaceID: step.WorkspaceID, InstanceID: step.InstanceID, CreatedOperationID: step.OperationID}); err != nil {
			return err
		}
		consumers, err := q.ListApplicationInstanceConsumers(ctx, db.ListApplicationInstanceConsumersParams{WorkspaceID: step.WorkspaceID, InstanceID: step.InstanceID})
		if err != nil {
			return err
		}
		if len(consumers) == 0 {
			instance, err := q.GetApplicationInstance(ctx, db.GetApplicationInstanceParams{ID: step.InstanceID, WorkspaceID: step.WorkspaceID})
			if err != nil {
				return err
			}
			if instance.Generation == step.Generation && instance.DesiredState == "running" {
				if _, err = q.SetApplicationInstanceDesiredState(ctx, db.SetApplicationInstanceDesiredStateParams{ID: step.InstanceID, WorkspaceID: step.WorkspaceID, DesiredState: "stopped"}); err != nil {
					return err
				}
			}
		}
	}
	steps, err := q.ListApplicationOperationSteps(ctx, db.ListApplicationOperationStepsParams{OperationID: step.OperationID, WorkspaceID: step.WorkspaceID})
	if err != nil {
		return err
	}
	for _, dependency := range command.Dependencies {
		used := false
		for _, other := range steps {
			if other.ID == step.ID || other.State == "failed" || other.State == "blocked" || other.State == "cancelled" {
				continue
			}
			if util.UUIDToString(other.InstanceID) == dependency.InstanceID {
				used = true
			}
			var otherCommand protocol.ApplicationControlCommand
			if err = json.Unmarshal(other.Command, &otherCommand); err != nil {
				return err
			}
			for _, otherDependency := range otherCommand.Dependencies {
				if otherDependency.InstanceID == dependency.InstanceID {
					used = true
				}
			}
		}
		if !used {
			id, parseErr := util.ParseUUID(dependency.InstanceID)
			if parseErr != nil {
				return parseErr
			}
			if err = q.ReleaseNewApplicationConsumer(ctx, db.ReleaseNewApplicationConsumerParams{WorkspaceID: step.WorkspaceID, InstanceID: id, CreatedOperationID: step.OperationID}); err != nil {
				return err
			}
		}
	}
	return nil
}
