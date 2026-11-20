package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type RevisionRef struct {
	WorkspaceID   pgtype.UUID
	ApplicationID pgtype.UUID
	Revision      int64
}

type RevisionView struct {
	WorkspaceID   string                     `json:"workspace_id"`
	ApplicationID string                     `json:"application_id"`
	Revision      int64                      `json:"revision"`
	Config        protocol.ApplicationConfig `json:"config"`
	Relations     []Relation                 `json:"relations"`
	ActorType     string                     `json:"actor_type"`
	ActorID       string                     `json:"actor_id"`
	CreatedAt     time.Time                  `json:"created_at"`
}

type RevisionPage struct {
	Revisions          []RevisionView `json:"revisions"`
	NextBeforeRevision *int64         `json:"next_before_revision"`
}

func decodeRevision(row db.ApplicationRevision) (RevisionView, error) {
	view := RevisionView{WorkspaceID: util.UUIDToString(row.WorkspaceID), ApplicationID: util.UUIDToString(row.ApplicationID), Revision: row.Revision, ActorType: row.ActorType, ActorID: util.UUIDToString(row.ActorID), CreatedAt: row.CreatedAt.Time, Relations: []Relation{}}
	if err := json.Unmarshal(row.Config, &view.Config); err != nil {
		return view, fmt.Errorf("decode application revision config: %w", err)
	}
	if err := json.Unmarshal(row.Relations, &view.Relations); err != nil {
		return view, fmt.Errorf("decode application revision relations: %w", err)
	}
	return view, nil
}

func (s *Service) ReadRevision(ctx context.Context, ref RevisionRef) (RevisionView, error) {
	row, err := s.Queries.GetApplicationRevision(ctx, db.GetApplicationRevisionParams{WorkspaceID: ref.WorkspaceID, ApplicationID: ref.ApplicationID, Revision: ref.Revision})
	if errors.Is(err, pgx.ErrNoRows) {
		return RevisionView{}, ErrNotFound
	}
	if err != nil {
		return RevisionView{}, fmt.Errorf("read application revision: %w", err)
	}
	return decodeRevision(row)
}

// Revision zero selects the latest page; otherwise the cursor is exclusive.
func (s *Service) RevisionHistory(ctx context.Context, before RevisionRef) (RevisionPage, error) {
	rows, err := s.Queries.ListApplicationRevisions(ctx, db.ListApplicationRevisionsParams{WorkspaceID: before.WorkspaceID, ApplicationID: before.ApplicationID, BeforeRevision: before.Revision})
	if err != nil {
		return RevisionPage{}, fmt.Errorf("list application revisions: %w", err)
	}
	page := RevisionPage{Revisions: make([]RevisionView, 0, len(rows))}
	if len(rows) > 25 {
		next := rows[24].Revision
		page.NextBeforeRevision = &next
		rows = rows[:25]
	}
	for _, row := range rows {
		view, err := decodeRevision(row)
		if err != nil {
			return RevisionPage{}, err
		}
		page.Revisions = append(page.Revisions, view)
	}
	return page, nil
}

type RestoreRevisionRequest struct {
	Target           RevisionRef
	ExpectedRevision int64
	Actor            Actor
}

// Restore makes a new revision and revalidates resources and relationships now.
func (s *Service) RestoreRevision(ctx context.Context, request RestoreRevisionRequest) (View, error) {
	target, err := s.ReadRevision(ctx, request.Target)
	if err != nil {
		return View{}, err
	}
	return s.Update(ctx, request.Target.WorkspaceID, request.Target.ApplicationID, UpdateInput{Revision: request.ExpectedRevision, Config: &target.Config, Relations: &target.Relations}, request.Actor)
}
