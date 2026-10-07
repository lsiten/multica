package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var (
	ErrInvalid   = errors.New("invalid application")
	ErrConflict  = errors.New("application conflict")
	ErrNotFound  = errors.New("application not found")
	ErrForbidden = errors.New("application access forbidden")
)

// TxStarter is supplied by the server's existing connection pool.
type TxStarter interface {
	Begin(context.Context) (pgx.Tx, error)
}

// Actor is resolved by authentication middleware, never accepted from request JSON.
type Actor struct {
	Type   string
	ID     pgtype.UUID
	UserID pgtype.UUID
	TaskID pgtype.UUID
}

// Service persists catalog revisions and serializes graph edits at the project boundary.
type Service struct {
	Queries      *db.Queries
	Transactions TxStarter
}

// View is the application catalog contract shared by UI, CLI and MCP.
type View struct {
	ID          string                     `json:"id"`
	WorkspaceID string                     `json:"workspace_id"`
	ProjectID   string                     `json:"project_id"`
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Kind        string                     `json:"kind"`
	Revision    int64                      `json:"revision"`
	CreatedBy   string                     `json:"created_by"`
	CreatedAt   time.Time                  `json:"created_at"`
	UpdatedAt   time.Time                  `json:"updated_at"`
	Config      protocol.ApplicationConfig `json:"config"`
	Relations   []Relation                 `json:"relations"`
}

// CreateInput defines a catalog application before any process is started.
type CreateInput struct {
	ProjectID   pgtype.UUID
	Name        string
	Description string
	Kind        string
	Config      protocol.ApplicationConfig
}

// UpdateInput uses revision matching to protect simultaneous human and agent edits.
type UpdateInput struct {
	Revision    int64                       `json:"revision"`
	Name        *string                     `json:"name,omitempty"`
	Description *string                     `json:"description,omitempty"`
	Config      *protocol.ApplicationConfig `json:"config,omitempty"`
	Relations   *[]Relation                 `json:"relations,omitempty"`
}

// List returns current definitions; running instances retain their own applied revision.
func (s *Service) List(ctx context.Context, workspaceID, projectID pgtype.UUID) ([]View, error) {
	rows, err := s.Queries.ListApplications(ctx, db.ListApplicationsParams{WorkspaceID: workspaceID, ProjectID: projectID})
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	views := make([]View, 0, len(rows))
	for _, row := range rows {
		view, err := decodeView(db.GetApplicationRow(row))
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

// Get reads one application strictly within the supplied workspace.
func (s *Service) Get(ctx context.Context, workspaceID, id pgtype.UUID) (View, error) {
	row, err := s.Queries.GetApplication(ctx, db.GetApplicationParams{ID: id, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, fmt.Errorf("get application: %w", err)
	}
	return decodeView(row)
}

func decodeView(row db.GetApplicationRow) (View, error) {
	view := View{ID: util.UUIDToString(row.ID), WorkspaceID: util.UUIDToString(row.WorkspaceID), ProjectID: util.UUIDToString(row.ProjectID), Name: row.Name, Description: row.Description, Kind: row.Kind, Revision: row.Revision, CreatedBy: util.UUIDToString(row.CreatedBy), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time, Relations: []Relation{}}
	if err := json.Unmarshal(row.Config, &view.Config); err != nil {
		return view, fmt.Errorf("decode application config: %w", err)
	}
	if err := json.Unmarshal(row.RevisionRelations, &view.Relations); err != nil {
		return view, fmt.Errorf("decode application relationships: %w", err)
	}
	return view, nil
}

func validCatalogInput(name, description string, config protocol.ApplicationConfig, kind string) error {
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 120 || strings.ContainsAny(name, "\x00\r\n") || len(description) > 4096 {
		return fmt.Errorf("%w: name must contain 1 to 120 characters and description at most 4096 bytes", ErrInvalid)
	}
	if err := config.Validate(kind); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

func validateActor(actor Actor) error {
	if !actor.ID.Valid || !actor.UserID.Valid || (actor.Type != "member" && actor.Type != "agent") || (actor.Type == "agent" && !actor.TaskID.Valid) {
		return ErrForbidden
	}
	return nil
}

func validateApplicationResource(ctx context.Context, q *db.Queries, workspaceID, projectID pgtype.UUID, config protocol.ApplicationConfig) error {
	if config.ResourceID == "" {
		return nil
	}
	resourceID, err := util.ParseUUID(config.ResourceID)
	if err != nil {
		return fmt.Errorf("%w: invalid resource_id", ErrInvalid)
	}
	resource, err := q.GetProjectResourceInWorkspace(ctx, db.GetProjectResourceInWorkspaceParams{ID: resourceID, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: resource not found", ErrInvalid)
	}
	if err != nil {
		return fmt.Errorf("load application resource: %w", err)
	}
	if resource.ProjectID != projectID {
		return fmt.Errorf("%w: resource belongs to another project", ErrInvalid)
	}
	return nil
}

// Create atomically writes a definition and its first immutable configuration revision.
func (s *Service) Create(ctx context.Context, workspaceID pgtype.UUID, input CreateInput, actor Actor) (View, error) {
	input.Name = strings.TrimSpace(input.Name)
	if err := validateActor(actor); err != nil {
		return View{}, err
	}
	if err := validCatalogInput(input.Name, input.Description, input.Config, input.Kind); err != nil {
		return View{}, err
	}
	tx, err := s.Transactions.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin application create: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	if err = lockCatalogProject(ctx, q, workspaceID, input.ProjectID); err != nil {
		return View{}, err
	}
	if err = validateApplicationResource(ctx, q, workspaceID, input.ProjectID, input.Config); err != nil {
		return View{}, err
	}
	row, err := q.CreateApplication(ctx, db.CreateApplicationParams{WorkspaceID: workspaceID, ProjectID: input.ProjectID, Name: input.Name, Description: input.Description, Kind: input.Kind, CreatedBy: actor.UserID})
	if err != nil {
		return View{}, fmt.Errorf("create application: %w", err)
	}
	config, err := json.Marshal(input.Config)
	if err != nil {
		return View{}, err
	}
	if _, err = q.InsertApplicationRevision(ctx, db.InsertApplicationRevisionParams{ApplicationID: row.ID, WorkspaceID: workspaceID, Revision: row.Revision, Config: config, Relations: []byte("[]"), ActorType: actor.Type, ActorID: actor.ID}); err != nil {
		return View{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit application create: %w", err)
	}
	return s.Get(ctx, workspaceID, row.ID)
}

// Update validates the whole project graph under one lock and commits a new revision.
func (s *Service) Update(ctx context.Context, workspaceID, id pgtype.UUID, input UpdateInput, actor Actor) (View, error) {
	if err := validateActor(actor); err != nil {
		return View{}, err
	}
	current, err := s.Get(ctx, workspaceID, id)
	if err != nil {
		return View{}, err
	}
	projectID, err := util.ParseUUID(current.ProjectID)
	if err != nil {
		return View{}, err
	}
	tx, err := s.Transactions.Begin(ctx)
	if err != nil {
		return View{}, err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	if err = lockCatalogProject(ctx, q, workspaceID, projectID); err != nil {
		return View{}, err
	}
	row, err := q.GetApplication(ctx, db.GetApplicationParams{ID: id, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, err
	}
	if row.Revision != input.Revision {
		return View{}, fmt.Errorf("%w: configuration revision changed", ErrConflict)
	}
	current, err = decodeView(row)
	if err != nil {
		return View{}, err
	}
	if input.Name != nil {
		current.Name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		current.Description = *input.Description
	}
	if input.Config != nil {
		current.Config = *input.Config
	}
	if input.Relations != nil {
		current.Relations = append([]Relation{}, (*input.Relations)...)
	}
	if err = validCatalogInput(current.Name, current.Description, current.Config, current.Kind); err != nil {
		return View{}, err
	}
	if err = validateApplicationResource(ctx, q, workspaceID, projectID, current.Config); err != nil {
		return View{}, err
	}
	nodes, relations, err := readGraph(ctx, q, workspaceID, projectID)
	if err != nil {
		return View{}, err
	}
	next := make([]Relation, 0, len(relations)+len(current.Relations))
	for _, relation := range relations {
		if relation.SourceID != current.ID {
			next = append(next, relation)
		}
	}
	for i := range current.Relations {
		relation := &current.Relations[i]
		if relation.SourceID != "" && relation.SourceID != current.ID {
			return View{}, fmt.Errorf("%w: relation source does not match application", ErrInvalid)
		}
		relation.SourceID = current.ID
		if _, err = util.ParseUUID(relation.TargetID); err != nil {
			return View{}, fmt.Errorf("%w: invalid target_id", ErrInvalid)
		}
		next = append(next, *relation)
	}
	if err = ValidateGraph(nodes, next); err != nil {
		return View{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err = q.ReplaceApplicationRelations(ctx, db.ReplaceApplicationRelationsParams{SourceID: id, WorkspaceID: workspaceID}); err != nil {
		return View{}, err
	}
	for _, relation := range current.Relations {
		targetID, parseErr := util.ParseUUID(relation.TargetID)
		if parseErr != nil {
			return View{}, parseErr
		}
		if _, err = q.InsertApplicationRelation(ctx, db.InsertApplicationRelationParams{WorkspaceID: workspaceID, ProjectID: projectID, SourceID: id, TargetID: targetID, Type: relation.Type, Required: relation.Required, Condition: relation.Condition, StartExternal: relation.StartExternal}); err != nil {
			return View{}, err
		}
	}
	updated, err := q.UpdateApplication(ctx, db.UpdateApplicationParams{ID: id, WorkspaceID: workspaceID, Name: current.Name, Description: current.Description, Revision: input.Revision})
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrConflict
	}
	if err != nil {
		return View{}, err
	}
	config, err := json.Marshal(current.Config)
	if err != nil {
		return View{}, err
	}
	relationJSON, err := json.Marshal(current.Relations)
	if err != nil {
		return View{}, err
	}
	if _, err = q.InsertApplicationRevision(ctx, db.InsertApplicationRevisionParams{ApplicationID: id, WorkspaceID: workspaceID, Revision: updated.Revision, Config: config, Relations: relationJSON, ActorType: actor.Type, ActorID: actor.ID}); err != nil {
		return View{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return View{}, err
	}
	return s.Get(ctx, workspaceID, id)
}

func readGraph(ctx context.Context, q *db.Queries, workspaceID, projectID pgtype.UUID) ([]Node, []Relation, error) {
	rows, err := q.ListApplications(ctx, db.ListApplicationsParams{WorkspaceID: workspaceID, ProjectID: projectID})
	if err != nil {
		return nil, nil, err
	}
	nodes := make([]Node, 0, len(rows))
	for _, row := range rows {
		nodes = append(nodes, Node{ID: util.UUIDToString(row.ID), WorkspaceID: util.UUIDToString(row.WorkspaceID), ProjectID: util.UUIDToString(row.ProjectID), Kind: row.Kind, Revision: row.Revision})
	}
	edges, err := q.ListApplicationRelations(ctx, db.ListApplicationRelationsParams{WorkspaceID: workspaceID, ProjectID: projectID})
	if err != nil {
		return nil, nil, err
	}
	relations := make([]Relation, 0, len(edges))
	for _, row := range edges {
		relations = append(relations, Relation{SourceID: util.UUIDToString(row.SourceID), TargetID: util.UUIDToString(row.TargetID), Type: row.Type, Required: row.Required, Condition: row.Condition, StartExternal: row.StartExternal})
	}
	return nodes, relations, nil
}

// Plan returns a read-only preview of the exact services a start would affect.
func (s *Service) Plan(ctx context.Context, workspaceID, id pgtype.UUID) (Plan, error) {
	view, err := s.Get(ctx, workspaceID, id)
	if err != nil {
		return Plan{}, err
	}
	projectID, err := util.ParseUUID(view.ProjectID)
	if err != nil {
		return Plan{}, err
	}
	tx, err := s.Transactions.Begin(ctx)
	if err != nil {
		return Plan{}, err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	if err = lockCatalogProject(ctx, q, workspaceID, projectID); err != nil {
		return Plan{}, err
	}
	nodes, relations, err := readGraph(ctx, q, workspaceID, projectID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := BuildPlan(view.ID, nodes, relations)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return plan, nil
}

// Delete refuses active instances or incoming relationships rather than silently breaking consumers.
func (s *Service) Delete(ctx context.Context, workspaceID, id pgtype.UUID, revision int64, actor Actor) error {
	if err := validateActor(actor); err != nil {
		return err
	}
	view, err := s.Get(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	projectID, err := util.ParseUUID(view.ProjectID)
	if err != nil {
		return err
	}
	tx, err := s.Transactions.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	if err = lockCatalogProject(ctx, q, workspaceID, projectID); err != nil {
		return err
	}
	current, err := q.GetApplication(ctx, db.GetApplicationParams{ID: id, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if current.Revision != revision {
		return ErrConflict
	}
	dependents, err := q.CountApplicationDependents(ctx, db.CountApplicationDependentsParams{WorkspaceID: workspaceID, TargetID: id})
	if err != nil {
		return err
	}
	active, err := q.CountApplicationActiveInstances(ctx, db.CountApplicationActiveInstancesParams{WorkspaceID: workspaceID, ApplicationID: id})
	if err != nil {
		return err
	}
	if dependents > 0 || active > 0 {
		return fmt.Errorf("%w: remove incoming relationships and stop instances before deleting", ErrConflict)
	}
	if err = q.DeleteApplicationInstanceConsumers(ctx, db.DeleteApplicationInstanceConsumersParams{WorkspaceID: workspaceID, RootApplicationID: id}); err != nil {
		return err
	}
	if err = q.DeleteApplicationTickets(ctx, db.DeleteApplicationTicketsParams{WorkspaceID: workspaceID, ApplicationID: id}); err != nil {
		return err
	}
	if err = q.DeleteApplicationOperationSteps(ctx, db.DeleteApplicationOperationStepsParams{WorkspaceID: workspaceID, ApplicationID: id}); err != nil {
		return err
	}
	if err = q.DeleteApplicationOperations(ctx, db.DeleteApplicationOperationsParams{WorkspaceID: workspaceID, ApplicationID: id}); err != nil {
		return err
	}
	if err = q.DeleteApplicationEndpoints(ctx, db.DeleteApplicationEndpointsParams{ApplicationID: id, WorkspaceID: workspaceID}); err != nil {
		return err
	}
	if err = q.DeleteApplicationInstances(ctx, db.DeleteApplicationInstancesParams{ApplicationID: id, WorkspaceID: workspaceID}); err != nil {
		return err
	}
	if err = q.DeleteApplicationRelations(ctx, db.DeleteApplicationRelationsParams{WorkspaceID: workspaceID, SourceID: id}); err != nil {
		return err
	}
	if err = q.DeleteApplicationRevisions(ctx, db.DeleteApplicationRevisionsParams{ApplicationID: id, WorkspaceID: workspaceID}); err != nil {
		return err
	}
	if err = q.DeleteApplication(ctx, db.DeleteApplicationParams{ID: id, WorkspaceID: workspaceID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
