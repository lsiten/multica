package service

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func progressTestRow(status string) db.ListProgressIssuesRow {
	at := pgtype.Timestamptz{Time: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), Valid: true}
	return db.ListProgressIssuesRow{ID: dbid.NewV7(), Number: 1, Title: status, Status: status, Priority: "none", CreatedAt: at, UpdatedAt: at, StatusChangedAt: at, InScope: true}
}

func progressTestBuild(t *testing.T, rows []db.ListProgressIssuesRow, dependencies []db.IssueDependency, scope ProgressScope) IssueProgressView {
	t.Helper()
	graph := newProgressGraph(rows, dependencies, nil, "MUL", "member")
	view, err := buildIssueProgress(t.Context(), graph, ProgressOptions{Scope: scope}, time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func progressTestEntry(t *testing.T, view IssueProgressView, id pgtype.UUID) ProgressEntry {
	t.Helper()
	for _, entry := range view.Items {
		if entry.Issue.ID == util.UUIDToString(id) {
			return entry
		}
	}
	t.Fatalf("issue %s missing from %+v", util.UUIDToString(id), view.Items)
	return ProgressEntry{}
}

func TestIssueProgressCountsEveryDescendantAndSeparatesParentWork(t *testing.T) {
	root, parent, leaf, done, closed := progressTestRow("in_progress"), progressTestRow("in_progress"), progressTestRow("todo"), progressTestRow("done"), progressTestRow("cancelled")
	parent.ParentIssueID, done.ParentIssueID, closed.ParentIssueID = root.ID, root.ID, root.ID
	leaf.ParentIssueID = parent.ID
	view := progressTestBuild(t, []db.ListProgressIssuesRow{root, parent, leaf, done, closed}, nil, ProgressScope{Type: "issue", ID: util.UUIDToString(root.ID)})
	if view.Summary.Total != 4 || view.Summary.Done != 1 || view.Summary.Closed != 1 || view.Summary.Open != 2 || view.Summary.OpenParent != 1 || view.Summary.OpenLeaf != 1 {
		t.Fatalf("summary: %+v", view.Summary)
	}
	if view.Root == nil || view.Root.WaitingChildren != 2 || slices.Contains(view.Root.Reasons, "stalled") {
		t.Fatalf("waiting parent: %+v", view.Root)
	}
	entry := progressTestEntry(t, view, leaf.ID)
	if len(entry.Path) != 2 || entry.Path[0].ID != util.UUIDToString(root.ID) || entry.Path[1].ID != util.UUIDToString(parent.ID) || entry.Group.ID != util.UUIDToString(parent.ID) {
		t.Fatalf("path and group: %+v", entry)
	}
}

func TestIssueProgressFollowsBothDependencyDirectionsAndDeduplicatesImpact(t *testing.T) {
	a, b, c := progressTestRow("todo"), progressTestRow("todo"), progressTestRow("todo")
	dependencies := []db.IssueDependency{{IssueID: a.ID, DependsOnIssueID: b.ID, Type: "blocked_by"}, {IssueID: c.ID, DependsOnIssueID: b.ID, Type: "blocks"}, {IssueID: b.ID, DependsOnIssueID: a.ID, Type: "blocks"}, {IssueID: a.ID, DependsOnIssueID: c.ID, Type: "related"}}
	view := progressTestBuild(t, []db.ListProgressIssuesRow{a, b, c}, dependencies, ProgressScope{Type: "project", ID: "project"})
	entry := progressTestEntry(t, view, a.ID)
	if len(entry.DirectBlockers) != 1 || len(entry.RootBlockers) != 1 || entry.RootBlockers[0].ID != util.UUIDToString(c.ID) {
		t.Fatalf("chain: %+v", entry)
	}
	if impact := progressTestEntry(t, view, c.ID).BlockedIssueCount; impact != 2 {
		t.Fatalf("distinct impact = %d, want 2", impact)
	}
}

func TestIssueProgressCancelledPrerequisiteRequiresDecision(t *testing.T) {
	a, b := progressTestRow("todo"), progressTestRow("cancelled")
	view := progressTestBuild(t, []db.ListProgressIssuesRow{a, b}, []db.IssueDependency{{IssueID: a.ID, DependsOnIssueID: b.ID, Type: "blocked_by"}}, ProgressScope{Type: "project", ID: "project"})
	entry := progressTestEntry(t, view, a.ID)
	if !slices.Contains(entry.Reasons, "cancelled_dependency") || len(entry.RootBlockers) != 1 || view.Summary.Closed != 1 {
		t.Fatalf("cancelled blocker: %+v; summary %+v", entry, view.Summary)
	}
}

func TestIssueProgressCyclesAreVisibleAndDoNotInventRootBlockers(t *testing.T) {
	a, b := progressTestRow("todo"), progressTestRow("todo")
	a.ParentIssueID, b.ParentIssueID = b.ID, a.ID
	dependencies := []db.IssueDependency{{IssueID: a.ID, DependsOnIssueID: b.ID, Type: "blocked_by"}, {IssueID: b.ID, DependsOnIssueID: a.ID, Type: "blocked_by"}}
	view := progressTestBuild(t, []db.ListProgressIssuesRow{a, b}, dependencies, ProgressScope{Type: "project", ID: "project"})
	entry := progressTestEntry(t, view, a.ID)
	if view.Complete || !slices.Contains(entry.Reasons, "dependency_cycle") || !slices.Contains(entry.Reasons, "hierarchy_cycle") || len(entry.RootBlockers) != 0 {
		t.Fatalf("cycles: %+v, coverage %v", entry, view.CoverageReasons)
	}
}

func TestIssueProgressStatusAndRunMatrix(t *testing.T) {
	cases := []struct {
		name         string
		row          db.ListProgressIssuesRow
		want, absent string
	}{
		{"member is not stalled", progressTestRow("in_progress"), "", "stalled"},
		{"parent needs wrap up", progressTestRow("in_progress"), "parent_wrap_up", "stalled"},
		{"failure rolled back to todo", progressTestRow("todo"), "run_failed", ""},
		{"active run supersedes failure", progressTestRow("in_progress"), "", "run_failed"},
		{"custom started is not review", progressTestRow("qa"), "", "review"},
		{"unknown state is not success", progressTestRow("future"), "status_unknown", ""},
		{"parent closed with open child", progressTestRow("done"), "parent_closed_with_open_children", ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			row := test.row
			rows := []db.ListProgressIssuesRow{row}
			switch test.name {
			case "member is not stalled":
				row.AssigneeType = pgtype.Text{String: "member", Valid: true}
				row.AssigneeID = dbid.NewV7()
			case "parent needs wrap up":
				child := progressTestRow("done")
				child.ParentIssueID = row.ID
				rows = append(rows, child)
				row.AssigneeType = pgtype.Text{String: "agent", Valid: true}
				row.AssigneeID = dbid.NewV7()
			case "failure rolled back to todo":
				row.LatestRunStatus = "failed"
			case "active run supersedes failure":
				row.LatestRunStatus = "failed"
				row.ActiveRunID = dbid.NewV7()
				row.ActiveRunStatus = "queued"
			case "custom started is not review":
				row.StatusCategory = "started"
			case "parent closed with open child":
				child := progressTestRow("todo")
				child.ParentIssueID = row.ID
				rows = append(rows, child)
			}
			rows[0] = row
			view := progressTestBuild(t, rows, nil, ProgressScope{Type: "project", ID: "project"})
			entry := progressTestEntry(t, view, row.ID)
			if test.want != "" && !slices.Contains(entry.Reasons, test.want) {
				t.Fatalf("reasons %v missing %s", entry.Reasons, test.want)
			}
			if test.absent != "" && slices.Contains(entry.Reasons, test.absent) {
				t.Fatalf("unexpected reason %s: %v", test.absent, entry.Reasons)
			}
		})
	}
}

func TestIssueProgressKnownCustomTerminalCategories(t *testing.T) {
	done, closed := progressTestRow("shipped"), progressTestRow("abandoned")
	done.StatusCategory, closed.StatusCategory = "done", "closed"
	view := progressTestBuild(t, []db.ListProgressIssuesRow{done, closed}, nil, ProgressScope{Type: "project", ID: "project"})
	if view.Summary.Done != 1 || view.Summary.Closed != 1 || view.Summary.Open != 0 || len(view.Items) != 0 || !view.Complete {
		t.Fatalf("terminal matrix: %+v", view)
	}
}

func TestIssueProgressStageHintsDoNotBlockAlreadyStartedWork(t *testing.T) {
	parent, first, later := progressTestRow("in_progress"), progressTestRow("todo"), progressTestRow("todo")
	first.ParentIssueID, later.ParentIssueID = parent.ID, parent.ID
	first.Stage = pgtype.Int4{Int32: 1, Valid: true}
	later.Stage = pgtype.Int4{Int32: 2, Valid: true}
	view := progressTestBuild(t, []db.ListProgressIssuesRow{parent, first, later}, nil, ProgressScope{Type: "issue", ID: util.UUIDToString(parent.ID)})
	if entry := progressTestEntry(t, view, later.ID); slices.Contains(entry.Reasons, "stage_waiting") || slices.Contains(entry.Reasons, "dependency") {
		t.Fatalf("invented barrier: %+v", entry)
	}
	later.Status = "backlog"
	view = progressTestBuild(t, []db.ListProgressIssuesRow{parent, first, later}, nil, ProgressScope{Type: "issue", ID: util.UUIDToString(parent.ID)})
	if entry := progressTestEntry(t, view, later.ID); !slices.Contains(entry.Reasons, "stage_waiting") {
		t.Fatalf("missing stage context: %+v", entry)
	}
	first.Status = "cancelled"
	view = progressTestBuild(t, []db.ListProgressIssuesRow{parent, first, later}, nil, ProgressScope{Type: "issue", ID: util.UUIDToString(parent.ID)})
	if entry := progressTestEntry(t, view, later.ID); !slices.Contains(entry.Reasons, "cancelled_stage") {
		t.Fatalf("cancelled stage: %+v", entry)
	}
}

func TestIssueProgressPagingUsesFullSummaryAndRejectsChangedSnapshots(t *testing.T) {
	rows := []db.ListProgressIssuesRow{progressTestRow("todo"), progressTestRow("todo"), progressTestRow("todo")}
	options := ProgressOptions{Scope: ProgressScope{Type: "project", ID: "project"}, Limit: 1, MemberID: "member"}
	view := progressTestBuild(t, rows, nil, options.Scope)
	first, err := paginateIssueProgress(view, options)
	if err != nil {
		t.Fatal(err)
	}
	if first.Summary.Total != 3 || len(first.Items) != 1 || !first.HasMore || first.NextCursor == nil {
		t.Fatalf("first page: %+v", first)
	}
	options.Cursor = *first.NextCursor
	second, err := paginateIssueProgress(view, options)
	if err != nil {
		t.Fatal(err)
	}
	if second.Items[0].Issue.ID == first.Items[0].Issue.ID {
		t.Fatal("page repeated issue")
	}
	changed := view
	changed.Items = append([]ProgressEntry{}, view.Items...)
	changed.Items[0].Issue.Title = "changed"
	if _, err = paginateIssueProgress(changed, options); !errors.Is(err, ErrProgressSnapshotChanged) {
		t.Fatalf("changed snapshot err=%v", err)
	}
	options.OnlyMine = true
	if _, err = paginateIssueProgress(view, options); !errors.Is(err, ErrProgressCursorInvalid) {
		t.Fatalf("cross-filter cursor err=%v", err)
	}
}

func TestIssueProgressCancellationStopsGraphBuild(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	graph := newProgressGraph([]db.ListProgressIssuesRow{progressTestRow("todo")}, nil, nil, "MUL", "member")
	if _, err := buildIssueProgress(ctx, graph, ProgressOptions{Scope: ProgressScope{Type: "project"}}, time.Now(), time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestIssueProgressSeparatesMissingRuntimeFromOfflineAndSupportsCustomUnstartedWork(t *testing.T) {
	row := progressTestRow("todo")
	row.AssigneeType = pgtype.Text{String: "agent", Valid: true}
	row.AssigneeID = dbid.NewV7()
	row.RuntimeMissing = true
	view := progressTestBuild(t, []db.ListProgressIssuesRow{row}, nil, ProgressScope{Type: "project", ID: "project"})
	entry := progressTestEntry(t, view, row.ID)
	if !slices.Contains(entry.Reasons, "runtime_missing") || slices.Contains(entry.Reasons, "runtime_offline") || slices.Contains(entry.Reasons, "ready") {
		t.Fatalf("missing runtime: %v", entry.Reasons)
	}
	row.RuntimeMissing = false
	row.RuntimeStatus = "offline"
	view = progressTestBuild(t, []db.ListProgressIssuesRow{row}, nil, ProgressScope{Type: "project", ID: "project"})
	entry = progressTestEntry(t, view, row.ID)
	if !slices.Contains(entry.Reasons, "runtime_offline") || slices.Contains(entry.Reasons, "runtime_missing") || slices.Contains(entry.Reasons, "ready") {
		t.Fatalf("offline runtime: %v", entry.Reasons)
	}
	row.RuntimeStatus = "online"
	row.Status = "ready_for_dev"
	row.StatusCategory = "unstarted"
	view = progressTestBuild(t, []db.ListProgressIssuesRow{row}, nil, ProgressScope{Type: "project", ID: "project"})
	if entry = progressTestEntry(t, view, row.ID); !slices.Contains(entry.Reasons, "ready") {
		t.Fatalf("custom unstarted work hidden: %v", entry.Reasons)
	}
}
