package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestReviewReuseRequiresSameScopeAndDirectory(t *testing.T) {
	identity := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	original := db.AgentTaskQueue{RuntimeID: identity, AgentID: identity, IssueID: identity, WorkDir: pgtype.Text{String: "/runtime/original/workdir", Valid: true}}
	for _, name := range []string{"verified", "runtime", "agent", "issue", "directory", "outside", "traversal", "missing_scope"} {
		t.Run(name, func(t *testing.T) {
			// Given independently loaded task metadata and an untrusted path.
			current := original
			selected := original.WorkDir.String + "/repo"
			switch name {
			case "runtime":
				current.RuntimeID.Bytes[0] = 2
			case "agent":
				current.AgentID.Bytes[0] = 2
			case "issue":
				current.IssueID.Bytes[0] = 2
			case "directory":
				current.WorkDir.String = "/runtime/other"
			case "outside":
				selected = original.WorkDir.String + "-other"
			case "traversal":
				selected += "/../../private"
			case "missing_scope":
				current.IssueID = pgtype.UUID{}
			}
			// When checking reuse, then only the same authorized scope succeeds.
			if got := reviewTasksShareDirectory(current, original, selected); got != (name == "verified") {
				t.Fatalf("accepted=%v", got)
			}
		})
	}
}
