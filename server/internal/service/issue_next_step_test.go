package service

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func TestCompletedRunIsNotFailedOrStalledAndHasInspectAction(t *testing.T) {
	row := progressTestRow("in_progress")
	row.AssigneeType = pgtype.Text{String: "agent", Valid: true}
	row.AssigneeID = dbid.NewV7()
	row.LatestRunID = dbid.NewV7()
	row.LatestRunStatus = "completed"
	row.LatestRunAt = row.CreatedAt
	row.LatestRunResult = []byte(`{"summary":"Delivered code; acceptance still pending"}`)
	view := progressTestBuild(t, []db.ListProgressIssuesRow{row}, nil, ProgressScope{Type: "project", ID: "project"})
	entry := progressTestEntry(t, view, row.ID)
	if slices.Contains(entry.Reasons, "stalled") || slices.Contains(entry.Reasons, "run_failed") || !slices.Contains(entry.Reasons, "run_ended_issue_open") || len(entry.Actions) != 1 || entry.Actions[0].Kind != "inspect_continue" {
		t.Fatalf("wrong completed-run action: %+v", entry)
	}
}

func TestNextStepIsBoundToItsReporterRevisionAndNamesMissingInformation(t *testing.T) {
	row := progressTestRow("in_progress")
	row.Revision = 5
	row.NextStepScope = "scope"
	row.LatestRunID = dbid.NewV7()
	row.LatestRunStatus = "completed"
	row.LatestRunAt = row.CreatedAt
	step := IssueNextStep{Kind: "needs_information", Summary: "Provide the test site", ActorType: "member", ActorID: "member", Missing: []string{"Test site URL"}, IssueRevision: 5, ScopeFingerprint: "scope", SourceTaskID: util.UUIDToString(row.LatestRunID)}
	raw, err := json.Marshal(map[string]any{"next_step": step})
	if err != nil {
		t.Fatal(err)
	}
	row.LatestRunContext = raw
	graph := newProgressGraph([]db.ListProgressIssuesRow{row}, nil, nil, "MUL", "member")
	entry := graph.entry(util.UUIDToString(row.ID), row.CreatedAt.Time, 0, ProgressScope{Type: "project", ID: "project"})
	if entry.NextStep == nil || !entry.NeedsMe || len(entry.Actions) != 1 || entry.Actions[0].Kind != "provide_info" {
		t.Fatalf("missing information handoff: %+v", entry)
	}
	row.Revision = 6
	if parseIssueNextStep(raw, row.Revision, util.UUIDToString(row.LatestRunID), "scope") == nil {
		t.Fatal("discussion-only revision lost the handoff")
	}
	if parseIssueNextStep(raw, row.Revision, util.UUIDToString(row.LatestRunID), "changed-scope") != nil {
		t.Fatal("old handoff was treated as current")
	}
}
