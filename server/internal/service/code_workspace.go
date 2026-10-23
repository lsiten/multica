package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var ErrInvalidCodeWorkspaceTree = errors.New("invalid code workspace issue tree")

// ResolveCodeWorkspaceRoot derives checkout ownership from the trusted issue
// tree. A broken ancestry must not silently create an unrelated checkout.
func ResolveCodeWorkspaceRoot(ctx context.Context, q *db.Queries, issue db.Issue) (db.Issue, error) {
	current := issue
	seen := map[pgtype.UUID]struct{}{issue.ID: {}}
	for hops := 0; current.ParentIssueID.Valid; hops++ {
		if hops >= 64 {
			return db.Issue{}, fmt.Errorf("%w: ancestry exceeds 64 parents", ErrInvalidCodeWorkspaceTree)
		}
		if _, exists := seen[current.ParentIssueID]; exists {
			return db.Issue{}, fmt.Errorf("%w: ancestry contains a cycle", ErrInvalidCodeWorkspaceTree)
		}
		seen[current.ParentIssueID] = struct{}{}
		parent, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
			ID: current.ParentIssueID, WorkspaceID: issue.WorkspaceID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Issue{}, fmt.Errorf("%w: parent is missing from the workspace", ErrInvalidCodeWorkspaceTree)
		}
		if err != nil {
			return db.Issue{}, fmt.Errorf("load code workspace parent: %w", err)
		}
		if parent.ProjectID != issue.ProjectID {
			return db.Issue{}, fmt.Errorf("%w: parent and child belong to different projects", ErrInvalidCodeWorkspaceTree)
		}
		current = parent
	}
	return current, nil
}
