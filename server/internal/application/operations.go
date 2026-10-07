package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// OperationInput specifies placement and optimistic revision for a durable control request.
type OperationInput struct {
	Action         string            `json:"action"`
	Revision       int64             `json:"revision"`
	RuntimeID      string            `json:"runtime_id"`
	Placements     map[string]string `json:"placements"`
	IdempotencyKey string            `json:"idempotency_key"`
	Force          bool              `json:"force"`
}

// ExecutionSnapshot records the graph and placements used by one operation.
type ExecutionSnapshot struct {
	RootRuntimeID string            `json:"root_runtime_id"`
	RootRevision  int64             `json:"root_revision"`
	Plan          Plan              `json:"plan"`
	Placements    map[string]string `json:"placements"`
}

// OperationStepView omits private commands and claim credentials from management responses.
type OperationStepView struct {
	ID            string `json:"id"`
	InstanceID    string `json:"instance_id"`
	ApplicationID string `json:"application_id"`
	RuntimeID     string `json:"runtime_id"`
	Generation    int64  `json:"generation"`
	Wave          int32  `json:"wave"`
	Required      bool   `json:"required"`
	Action        string `json:"action"`
	State         string `json:"state"`
	Error         string `json:"error"`
}

// OperationView separates accepting an operation from confirming a service's actual result.
type OperationView struct {
	ID                string              `json:"id"`
	WorkspaceID       string              `json:"workspace_id"`
	ApplicationID     string              `json:"application_id"`
	Action            string              `json:"action"`
	ActorType         string              `json:"actor_type"`
	ActorID           string              `json:"actor_id"`
	State             string              `json:"state"`
	Error             string              `json:"error"`
	CreatedAt         time.Time           `json:"created_at"`
	CompletedAt       *time.Time          `json:"completed_at"`
	DeadlineAt        time.Time           `json:"deadline_at"`
	CancelRequestedAt *time.Time          `json:"cancel_requested_at"`
	CancelActorType   string              `json:"cancel_actor_type"`
	CancelActorID     string              `json:"cancel_actor_id"`
	Snapshot          ExecutionSnapshot   `json:"snapshot"`
	Steps             []OperationStepView `json:"steps"`
}

func operationView(ctx context.Context, q *db.Queries, row db.ApplicationOperation) (OperationView, error) {
	view := OperationView{ID: util.UUIDToString(row.ID), WorkspaceID: util.UUIDToString(row.WorkspaceID), ApplicationID: util.UUIDToString(row.ApplicationID), Action: row.Action, ActorType: row.ActorType, ActorID: util.UUIDToString(row.ActorID), State: row.State, Error: row.Error, CreatedAt: row.CreatedAt.Time, Steps: []OperationStepView{}}
	view.DeadlineAt = row.DeadlineAt.Time
	if row.CompletedAt.Valid {
		view.CompletedAt = &row.CompletedAt.Time
	}
	if row.CancelRequestedAt.Valid {
		view.CancelRequestedAt = &row.CancelRequestedAt.Time
		view.CancelActorType = row.CancelActorType.String
		view.CancelActorID = util.UUIDToString(row.CancelActorID)
	}
	if err := json.Unmarshal(row.Plan, &view.Snapshot); err != nil {
		return view, fmt.Errorf("decode application operation snapshot: %w", err)
	}
	steps, err := q.ListApplicationOperationSteps(ctx, db.ListApplicationOperationStepsParams{OperationID: row.ID, WorkspaceID: row.WorkspaceID})
	if err != nil {
		return view, err
	}
	for _, step := range steps {
		view.Steps = append(view.Steps, OperationStepView{ID: util.UUIDToString(step.ID), InstanceID: util.UUIDToString(step.InstanceID), ApplicationID: util.UUIDToString(step.ApplicationID), RuntimeID: util.UUIDToString(step.RuntimeID), Generation: step.Generation, Wave: step.Wave, Required: step.Required, Action: step.Action, State: step.State, Error: step.Error})
	}
	return view, nil
}

// GetOperation reads one immutable execution snapshot and its current progress.
func (s *Service) GetOperation(ctx context.Context, workspaceID, id pgtype.UUID) (OperationView, error) {
	row, err := s.Queries.GetApplicationOperation(ctx, db.GetApplicationOperationParams{ID: id, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return OperationView{}, ErrNotFound
	}
	if err != nil {
		return OperationView{}, err
	}
	return operationView(ctx, s.Queries, row)
}

// ListOperations returns bounded recent activity for the selected application.
func (s *Service) ListOperations(ctx context.Context, workspaceID, applicationID pgtype.UUID) ([]OperationView, error) {
	rows, err := s.Queries.ListApplicationOperations(ctx, db.ListApplicationOperationsParams{WorkspaceID: workspaceID, ApplicationID: applicationID})
	if err != nil {
		return nil, err
	}
	views := make([]OperationView, 0, len(rows))
	for _, row := range rows {
		view, viewErr := operationView(ctx, s.Queries, row)
		if viewErr != nil {
			return nil, viewErr
		}
		views = append(views, view)
	}
	return views, nil
}

func validateOperationInput(input OperationInput) error {
	if !slices.Contains([]string{"start", "stop", "restart", "publish", "unpublish"}, input.Action) {
		return fmt.Errorf("%w: invalid operation action", ErrInvalid)
	}
	if input.Revision < 1 || len(input.IdempotencyKey) < 1 || len(input.IdempotencyKey) > 128 || strings.ContainsAny(input.IdempotencyKey, "\x00\r\n") {
		return fmt.Errorf("%w: revision and bounded idempotency_key are required", ErrInvalid)
	}
	if len(input.Placements) > 256 {
		return fmt.Errorf("%w: too many runtime placements", ErrInvalid)
	}
	if input.Force && input.Action != "stop" && input.Action != "restart" {
		return fmt.Errorf("%w: force is only supported for stop and restart", ErrInvalid)
	}
	return nil
}

func operationRuntime(ctx context.Context, q *db.Queries, workspaceID, runtimeID pgtype.UUID, actor Actor) (db.AgentRuntime, error) {
	rt, err := q.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{ID: runtimeID, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return rt, ErrNotFound
	}
	if err != nil {
		return rt, err
	}
	if !rt.OwnerID.Valid || !rt.DaemonID.Valid || (rt.OwnerID != actor.UserID && rt.Visibility != "public") {
		return rt, ErrForbidden
	}
	var metadata struct {
		Capabilities []string `json:"capabilities"`
	}
	if err = json.Unmarshal(rt.Metadata, &metadata); err != nil || !slices.Contains(metadata.Capabilities, protocol.DaemonCapabilityApplicationsV1) {
		return rt, fmt.Errorf("%w: runtime must be upgraded to support applications", ErrConflict)
	}
	if _, err = util.ParseUUID(rt.DaemonID.String); err != nil {
		return rt, fmt.Errorf("%w: runtime has no stable daemon identity", ErrConflict)
	}
	return rt, nil
}

func commandResource(ctx context.Context, q *db.Queries, workspaceID, projectID pgtype.UUID, config protocol.ApplicationConfig, runtime db.AgentRuntime) (string, json.RawMessage, error) {
	if config.ResourceID == "" {
		return "", nil, nil
	}
	if err := validateApplicationResource(ctx, q, workspaceID, projectID, config); err != nil {
		return "", nil, err
	}
	id, err := util.ParseUUID(config.ResourceID)
	if err != nil {
		return "", nil, err
	}
	resource, err := q.GetProjectResourceInWorkspace(ctx, db.GetProjectResourceInWorkspaceParams{ID: id, WorkspaceID: workspaceID})
	if err != nil {
		return "", nil, err
	}
	if resource.ResourceType == "local_directory" {
		var local struct {
			DaemonID string `json:"daemon_id"`
		}
		if err = json.Unmarshal(resource.ResourceRef, &local); err != nil {
			return "", nil, err
		}
		if local.DaemonID != runtime.DaemonID.String {
			return "", nil, fmt.Errorf("%w: local resource belongs to a different machine", ErrInvalid)
		}
	}
	return resource.ResourceType, json.RawMessage(resource.ResourceRef), nil
}

// Enqueue freezes configuration, authorizes every runtime and atomically reserves instances.
func (s *Service) Enqueue(ctx context.Context, workspaceID, id pgtype.UUID, input OperationInput, actor Actor) (OperationView, error) {
	if err := validateActor(actor); err != nil {
		return OperationView{}, err
	}
	if err := validateOperationInput(input); err != nil {
		return OperationView{}, err
	}
	rootRuntimeID, err := util.ParseUUID(input.RuntimeID)
	if err != nil {
		return OperationView{}, fmt.Errorf("%w: runtime_id must be a UUID", ErrInvalid)
	}
	root, err := s.Get(ctx, workspaceID, id)
	if err != nil {
		return OperationView{}, err
	}
	projectID, err := util.ParseUUID(root.ProjectID)
	if err != nil {
		return OperationView{}, err
	}
	request, err := json.Marshal(struct {
		ApplicationID string
		ActorType     string
		ActorID       string
		Input         OperationInput
	}{root.ID, actor.Type, util.UUIDToString(actor.ID), input})
	if err != nil {
		return OperationView{}, err
	}
	hash := sha256.Sum256(request)
	requestHash := hex.EncodeToString(hash[:])
	tx, err := s.Transactions.Begin(ctx)
	if err != nil {
		return OperationView{}, err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	if err = lockCatalogProject(ctx, q, workspaceID, projectID); err != nil {
		return OperationView{}, err
	}
	previous, err := q.GetApplicationOperationByKey(ctx, db.GetApplicationOperationByKeyParams{WorkspaceID: workspaceID, UserID: actor.UserID, IdempotencyKey: input.IdempotencyKey})
	if err == nil {
		if previous.RequestHash != requestHash {
			return OperationView{}, fmt.Errorf("%w: idempotency key already identifies a different request", ErrConflict)
		}
		return operationView(ctx, q, previous)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OperationView{}, err
	}
	row, err := q.GetApplication(ctx, db.GetApplicationParams{ID: id, WorkspaceID: workspaceID})
	if err != nil {
		return OperationView{}, err
	}
	if row.Revision != input.Revision {
		return OperationView{}, fmt.Errorf("%w: application revision changed", ErrConflict)
	}
	if err = lockOperationRuntimes(ctx, q, workspaceID, id, rootRuntimeID, input.Placements); err != nil {
		return OperationView{}, err
	}
	if _, err = operationRuntime(ctx, q, workspaceID, rootRuntimeID, actor); err != nil {
		return OperationView{}, err
	}
	nodes, relations, err := readGraph(ctx, q, workspaceID, projectID)
	if err != nil {
		return OperationView{}, err
	}
	plan, err := BuildPlan(root.ID, nodes, relations)
	if err != nil && input.Action != "stop" && input.Action != "unpublish" {
		return OperationView{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	snapshot := ExecutionSnapshot{RootRuntimeID: util.UUIDToString(rootRuntimeID), RootRevision: row.Revision, Plan: plan, Placements: map[string]string{}}
	allowedPlacements := make(map[string]bool)
	for _, node := range plan.Nodes {
		allowedPlacements[node.ID] = true
		for _, dep := range node.Dependencies {
			allowedPlacements[dep.ID] = true
		}
	}
	for appID, runtime := range input.Placements {
		if !allowedPlacements[appID] {
			return OperationView{}, fmt.Errorf("%w: placement is outside the composition", ErrInvalid)
		}
		if _, err = util.ParseUUID(runtime); err != nil {
			return OperationView{}, fmt.Errorf("%w: invalid runtime placement", ErrInvalid)
		}
	}
	for appID := range allowedPlacements {
		runtime := input.RuntimeID
		if placed, ok := input.Placements[appID]; ok {
			runtime = placed
		}
		snapshot.Placements[appID] = runtime
	}
	planJSON, err := json.Marshal(snapshot)
	if err != nil {
		return OperationView{}, err
	}
	operation, err := q.CreateApplicationOperation(ctx, db.CreateApplicationOperationParams{WorkspaceID: workspaceID, ApplicationID: id, Action: input.Action, ActorType: actor.Type, ActorID: actor.ID, UserID: actor.UserID, TaskID: actor.TaskID, IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash, Plan: planJSON})
	if err != nil {
		return OperationView{}, err
	}
	if input.Action == "stop" || input.Action == "unpublish" {
		err = enqueueRelease(ctx, q, workspaceID, id, rootRuntimeID, input, actor, operation)
	} else {
		err = enqueuePlan(ctx, q, workspaceID, projectID, id, rootRuntimeID, input, actor, operation, snapshot)
	}
	if err != nil {
		return OperationView{}, err
	}
	steps, err := q.ListApplicationOperationSteps(ctx, db.ListApplicationOperationStepsParams{OperationID: operation.ID, WorkspaceID: workspaceID})
	if err != nil {
		return OperationView{}, err
	}
	deadline, err := operationDeadline(operation.CreatedAt.Time, steps)
	if err != nil {
		return OperationView{}, err
	}
	if err = q.SetApplicationOperationDeadline(ctx, db.SetApplicationOperationDeadlineParams{ID: operation.ID, WorkspaceID: workspaceID, DeadlineAt: pgtype.Timestamptz{Time: deadline, Valid: true}}); err != nil {
		return OperationView{}, err
	}
	if err = refreshOperationState(ctx, q, operation); err != nil {
		return OperationView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return OperationView{}, err
	}
	return s.GetOperation(ctx, workspaceID, operation.ID)
}

func enqueuePlan(ctx context.Context, q *db.Queries, workspaceID, projectID, rootID, rootRuntimeID pgtype.UUID, input OperationInput, actor Actor, operation db.ApplicationOperation, snapshot ExecutionSnapshot) error {
	member, err := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{WorkspaceID: workspaceID, UserID: actor.UserID})
	if err != nil {
		return err
	}
	instances := make(map[string]db.ApplicationInstance)
	commands := make(map[string]protocol.ApplicationControlCommand)
	waves := make(map[string]int)
	for wave, nodes := range snapshot.Plan.Waves {
		for _, id := range nodes {
			waves[id] = wave
		}
	}
	for _, node := range snapshot.Plan.Nodes {
		appID, err := util.ParseUUID(node.ID)
		if err != nil {
			return err
		}
		runtimeID, err := util.ParseUUID(snapshot.Placements[node.ID])
		if err != nil {
			return err
		}
		rt, err := operationRuntime(ctx, q, workspaceID, runtimeID, actor)
		if err != nil {
			return err
		}
		revision, err := q.GetApplicationRevision(ctx, db.GetApplicationRevisionParams{ApplicationID: appID, WorkspaceID: workspaceID, Revision: node.Revision})
		if err != nil {
			return err
		}
		var config protocol.ApplicationConfig
		if err = json.Unmarshal(revision.Config, &config); err != nil {
			return err
		}
		if input.Action == "restart" && config.Mode == "external" {
			return fmt.Errorf("%w: external services cannot be restarted", ErrInvalid)
		}
		if err = config.Validate("service"); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		resourceType, resourceRef, err := commandResource(ctx, q, workspaceID, projectID, config, rt)
		if err != nil {
			return err
		}
		instance, err := q.GetApplicationInstanceByRuntime(ctx, db.GetApplicationInstanceByRuntimeParams{ApplicationID: appID, RuntimeID: runtimeID, WorkspaceID: workspaceID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if instance.ID.Valid {
			pending, pendingErr := q.CountApplicationInstanceOperations(ctx, db.CountApplicationInstanceOperationsParams{InstanceID: instance.ID, WorkspaceID: workspaceID})
			if pendingErr != nil {
				return pendingErr
			}
			if pending > 0 {
				return fmt.Errorf("%w: instance already has a pending operation", ErrConflict)
			}
			if instance.DesiredState == "running" && instance.Revision != node.Revision && input.Action != "restart" {
				return fmt.Errorf("%w: restart the instance to apply the changed configuration", ErrConflict)
			}
			if input.Action == "restart" {
				consumers, consumerErr := q.ListApplicationInstanceConsumers(ctx, db.ListApplicationInstanceConsumersParams{WorkspaceID: workspaceID, InstanceID: instance.ID})
				if consumerErr != nil {
					return consumerErr
				}
				for _, consumer := range consumers {
					if (consumer.RootApplicationID != rootID || consumer.RootRuntimeID != rootRuntimeID) && !input.Force {
						return fmt.Errorf("%w: restart would interrupt another application consumer", ErrConflict)
					}
				}
			}
		}
		if input.Action == "publish" && config.Port == 0 {
			continue
		}
		if input.Action == "publish" && (!instance.ID.Valid || instance.DesiredState != "running") {
			return fmt.Errorf("%w: start services before publishing", ErrConflict)
		}
		if !instance.ID.Valid || input.Action == "restart" || instance.Revision != node.Revision {
			daemonID, parseErr := util.ParseUUID(rt.DaemonID.String)
			if parseErr != nil {
				return parseErr
			}
			instance, err = q.UpsertApplicationInstance(ctx, db.UpsertApplicationInstanceParams{WorkspaceID: workspaceID, ApplicationID: appID, RuntimeID: runtimeID, DaemonID: daemonID, Revision: node.Revision})
			if err != nil {
				return err
			}
		}
		confirmedExit := instance.ObservedGeneration == instance.Generation && (instance.ProcessState == "stopped" || instance.ProcessState == "failed")
		if input.Action != "publish" && (instance.DesiredState != "running" || input.Action == "restart" || confirmedExit) {
			instance, err = q.SetApplicationInstanceDesiredState(ctx, db.SetApplicationInstanceDesiredStateParams{ID: instance.ID, WorkspaceID: workspaceID, DesiredState: "running"})
			if err != nil {
				return err
			}
		}
		command := protocol.ApplicationControlCommand{InstanceID: util.UUIDToString(instance.ID), ApplicationID: node.ID, WorkspaceID: util.UUIDToString(workspaceID), RuntimeID: util.UUIDToString(runtimeID), RootApplicationID: util.UUIDToString(rootID), RootRuntimeID: util.UUIDToString(rootRuntimeID), Revision: instance.Revision, Generation: instance.Generation, Action: input.Action, Config: config, ResourceType: resourceType, ResourceRef: resourceRef, Dependencies: []protocol.ApplicationInstanceDependency{}}
		command.AccessUserID = util.UUIDToString(actor.UserID)
		command.AccessMemberID = util.UUIDToString(member.ID)
		if actor.Type == "agent" {
			command.AccessAgentID = util.UUIDToString(actor.ID)
		}
		if input.Action != "publish" {
			consumer, consumerErr := q.AddApplicationInstanceConsumer(ctx, db.AddApplicationInstanceConsumerParams{WorkspaceID: workspaceID, InstanceID: instance.ID, RootApplicationID: rootID, RootRuntimeID: rootRuntimeID, ActorID: actor.ID, Wave: int32(waves[node.ID]), CreatedOperationID: operation.ID, OwnsLifecycle: true})
			if consumerErr != nil {
				return consumerErr
			}
			command.ConsumerCreated = consumer.Inserted
		}
		instances[node.ID] = instance
		commands[node.ID] = command
	}
	if len(commands) == 0 {
		return fmt.Errorf("%w: no service in this application has a publishable port", ErrInvalid)
	}
	for _, node := range snapshot.Plan.Nodes {
		command, exists := commands[node.ID]
		if !exists {
			continue
		}
		for _, dependency := range node.Dependencies {
			instance, internal := instances[dependency.ID]
			if !internal {
				appID, err := util.ParseUUID(dependency.ID)
				if err != nil {
					return err
				}
				runtimeID, err := util.ParseUUID(snapshot.Placements[dependency.ID])
				if err != nil {
					return err
				}
				if _, err = operationRuntime(ctx, q, workspaceID, runtimeID, actor); err != nil {
					return err
				}
				instance, err = q.GetApplicationInstanceByRuntime(ctx, db.GetApplicationInstanceByRuntimeParams{ApplicationID: appID, RuntimeID: runtimeID, WorkspaceID: workspaceID})
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("%w: external dependency has no instance on the selected runtime", ErrConflict)
				}
				if err != nil {
					return err
				}
				if input.Action != "publish" {
					if _, err = q.AddApplicationInstanceConsumer(ctx, db.AddApplicationInstanceConsumerParams{WorkspaceID: workspaceID, InstanceID: instance.ID, RootApplicationID: rootID, RootRuntimeID: rootRuntimeID, ActorID: actor.ID, Wave: -1, CreatedOperationID: operation.ID, OwnsLifecycle: false}); err != nil {
						return err
					}
				}
			}
			command.Dependencies = append(command.Dependencies, protocol.ApplicationInstanceDependency{InstanceID: util.UUIDToString(instance.ID), Generation: instance.Generation, Condition: dependency.Condition})
			for _, binding := range command.Config.Connections {
				if binding.TargetID != dependency.ID {
					continue
				}
				if err := resolveApplicationConnection(ctx, q, &command, instances[node.ID], instance, binding); err != nil {
					return err
				}
			}
		}
		if len(command.Connections) != len(command.Config.Connections) {
			return fmt.Errorf("%w: every application connection must target a declared service dependency", ErrInvalid)
		}
		raw, err := json.Marshal(command)
		if err != nil {
			return err
		}
		instance := instances[node.ID]
		if _, err = q.InsertApplicationOperationStep(ctx, db.InsertApplicationOperationStepParams{WorkspaceID: workspaceID, OperationID: operation.ID, InstanceID: instance.ID, ApplicationID: instance.ApplicationID, RuntimeID: instance.RuntimeID, Generation: instance.Generation, Wave: int32(waves[node.ID]), Required: node.Required, Action: input.Action, Command: raw}); err != nil {
			return err
		}
	}
	return nil
}

func enqueueRelease(ctx context.Context, q *db.Queries, workspaceID, rootID, rootRuntimeID pgtype.UUID, input OperationInput, actor Actor, operation db.ApplicationOperation) error {
	instances, err := q.ListApplicationRootInstances(ctx, db.ListApplicationRootInstancesParams{WorkspaceID: workspaceID, RootApplicationID: rootID, RootRuntimeID: rootRuntimeID})
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		instance, getErr := q.GetApplicationInstanceByRuntime(ctx, db.GetApplicationInstanceByRuntimeParams{ApplicationID: rootID, RuntimeID: rootRuntimeID, WorkspaceID: workspaceID})
		if getErr != nil && !errors.Is(getErr, pgx.ErrNoRows) {
			return getErr
		}
		if getErr == nil {
			instances = append(instances, instance)
		}
	}
	for wave, instance := range instances {
		if _, err = operationRuntime(ctx, q, workspaceID, instance.RuntimeID, actor); err != nil {
			return err
		}
		pending, pendingErr := q.CountApplicationInstanceOperations(ctx, db.CountApplicationInstanceOperationsParams{InstanceID: instance.ID, WorkspaceID: workspaceID})
		if pendingErr != nil {
			return pendingErr
		}
		if pending > 0 {
			return fmt.Errorf("%w: instance already has a pending operation", ErrConflict)
		}
		consumers, consumerErr := q.ListApplicationInstanceConsumers(ctx, db.ListApplicationInstanceConsumersParams{WorkspaceID: workspaceID, InstanceID: instance.ID})
		if consumerErr != nil {
			return consumerErr
		}
		shared := false
		ownsLifecycle := false
		for _, consumer := range consumers {
			if consumer.RootApplicationID != rootID || consumer.RootRuntimeID != rootRuntimeID {
				shared = true
			} else {
				ownsLifecycle = ownsLifecycle || consumer.OwnsLifecycle
			}
		}
		if instance.ApplicationID == rootID {
			ownsLifecycle = true
		}
		if !ownsLifecycle {
			if err = q.ReleaseApplicationConsumer(ctx, db.ReleaseApplicationConsumerParams{WorkspaceID: workspaceID, InstanceID: instance.ID, RootApplicationID: rootID, RootRuntimeID: rootRuntimeID}); err != nil {
				return err
			}
			continue
		}
		if input.Action == "stop" && shared && !input.Force {
			if instance.ApplicationID == rootID {
				return fmt.Errorf("%w: service is still used by another application", ErrConflict)
			}
			if err = q.ReleaseApplicationConsumer(ctx, db.ReleaseApplicationConsumerParams{WorkspaceID: workspaceID, InstanceID: instance.ID, RootApplicationID: rootID, RootRuntimeID: rootRuntimeID}); err != nil {
				return err
			}
			continue
		}
		revision, revisionErr := q.GetApplicationRevision(ctx, db.GetApplicationRevisionParams{ApplicationID: instance.ApplicationID, WorkspaceID: workspaceID, Revision: instance.Revision})
		if revisionErr != nil {
			return revisionErr
		}
		var config protocol.ApplicationConfig
		if err = json.Unmarshal(revision.Config, &config); err != nil {
			return err
		}
		if input.Action == "stop" {
			instance, err = q.SetApplicationInstanceDesiredState(ctx, db.SetApplicationInstanceDesiredStateParams{ID: instance.ID, WorkspaceID: workspaceID, DesiredState: "stopped"})
			if err != nil {
				return err
			}
		}
		if input.Action == "unpublish" {
			if err = q.SetApplicationEndpointState(ctx, db.SetApplicationEndpointStateParams{InstanceID: instance.ID, WorkspaceID: workspaceID, State: "unpublished"}); err != nil {
				return err
			}
		}
		command := protocol.ApplicationControlCommand{InstanceID: util.UUIDToString(instance.ID), ApplicationID: util.UUIDToString(instance.ApplicationID), WorkspaceID: util.UUIDToString(workspaceID), RuntimeID: util.UUIDToString(instance.RuntimeID), RootApplicationID: util.UUIDToString(rootID), RootRuntimeID: util.UUIDToString(rootRuntimeID), Revision: instance.Revision, Generation: instance.Generation, Action: input.Action, Config: config, Dependencies: []protocol.ApplicationInstanceDependency{}}
		raw, marshalErr := json.Marshal(command)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = q.InsertApplicationOperationStep(ctx, db.InsertApplicationOperationStepParams{WorkspaceID: workspaceID, OperationID: operation.ID, InstanceID: instance.ID, ApplicationID: instance.ApplicationID, RuntimeID: instance.RuntimeID, Generation: instance.Generation, Wave: int32(wave), Required: true, Action: input.Action, Command: raw}); err != nil {
			return err
		}
	}
	return nil
}

func refreshOperationState(ctx context.Context, q *db.Queries, operation db.ApplicationOperation) error {
	steps, err := q.ListApplicationOperationSteps(ctx, db.ListApplicationOperationStepsParams{OperationID: operation.ID, WorkspaceID: operation.WorkspaceID})
	if err != nil {
		return err
	}
	state, message := "completed", ""
	failed, optionalFailure, pending, running := false, false, false, false
	for _, step := range steps {
		if step.State != "queued" {
			running = true
		}
		switch step.State {
		case "queued":
			pending = true
		case "running":
			pending = true
			running = true
		case "failed", "blocked":
			if step.Required {
				failed = true
			} else {
				optionalFailure = true
			}
			if message == "" {
				message = step.Error
			}
		}
	}
	if pending {
		state = "queued"
		if running {
			state = "running"
		}
	} else if failed {
		state = "failed"
	} else if optionalFailure {
		state = "partial"
	}
	if operation.CancelRequestedAt.Valid {
		state = "cancelled"
		if pending {
			state = "cancelling"
		} else if failed {
			state = "failed"
		}
	}
	return q.SetApplicationOperationState(ctx, db.SetApplicationOperationStateParams{ID: operation.ID, WorkspaceID: operation.WorkspaceID, State: state, Error: message})
}
