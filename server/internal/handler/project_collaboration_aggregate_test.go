package handler

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func collaborationUUID(seed byte) pgtype.UUID {
	var u pgtype.UUID
	u.Bytes[15] = seed
	u.Valid = true
	return u
}
func collaborationTime() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Unix(10, 0).UTC(), Valid: true}
}

func TestCollaborationTaskActiveUsesIssueStateBeforeRunState(t *testing.T) {
	issue := collaborationUUID(21)
	if !collaborationTaskActive(db.ListProjectCollaborationRunsRow{IssueID: issue, Status: "completed", TaskActive: true}) {
		t.Fatal("a completed run on an unfinished issue must remain active")
	}
	if collaborationTaskActive(db.ListProjectCollaborationRunsRow{IssueID: issue, Status: "running", TaskActive: false}) {
		t.Fatal("a terminal issue must not be active solely because a stale run is running")
	}
	if collaborationTaskActive(db.ListProjectCollaborationRunsRow{Status: "running"}) {
		t.Fatal("runs without a project issue must not define unfinished project tasks")
	}
}
func TestBuildProjectCollaborationDeduplicatesEventsAndSkipsSelfEdges(t *testing.T) {
	parent, child := collaborationUUID(1), collaborationUUID(2)
	rows := []db.ListProjectCollaborationRunsRow{
		{ID: collaborationUUID(3), IssueID: collaborationUUID(43), AgentID: parent, AgentName: "lead", Status: "completed", CreatedAt: collaborationTime(), RelationType: "root"},
		{ID: collaborationUUID(4), IssueID: collaborationUUID(44), AgentID: child, AgentName: "worker", Status: "running", TaskActive: true, CreatedAt: collaborationTime(), SourceTaskID: parent, SourceAgentID: parent, SourceAgentName: "lead", RelationType: "delegated"},
		{ID: collaborationUUID(5), IssueID: collaborationUUID(45), AgentID: child, AgentName: "worker", Status: "running", TaskActive: true, CreatedAt: collaborationTime(), SourceTaskID: parent, SourceAgentID: parent, SourceAgentName: "lead", RelationType: "delegated"},
		{ID: collaborationUUID(6), IssueID: collaborationUUID(46), AgentID: child, AgentName: "worker", Status: "running", TaskActive: true, CreatedAt: collaborationTime(), SourceTaskID: child, SourceAgentID: child, RelationType: "retry"},
	}
	graph := buildProjectCollaboration(projectCollaborationDataset{ProjectID: "p", AsOf: time.Now().UTC().Format(time.RFC3339Nano), Runs: rows, projectCollaborationPage: projectCollaborationPage{Limit: 100}})
	if len(graph.Edges) != 1 || graph.Edges[0].Count != 2 || graph.Edges[0].EvidenceCount != 2 {
		t.Fatalf("edges=%+v", graph.Edges)
	}
	if graph.Summary.AgentCount != 2 || graph.Summary.ActiveCount != 3 {
		t.Fatalf("summary=%+v", graph.Summary)
	}
}
func TestCollaborationPageBounds(t *testing.T) {
	for _, tc := range []struct{ n, l, o, s, e int }{{10, 3, 0, 0, 3}, {10, 3, 8, 8, 10}, {10, 3, 20, 10, 10}} {
		s, e := collaborationPageBounds(tc.n, projectCollaborationPage{Limit: tc.l, Offset: tc.o})
		if s != tc.s || e != tc.e {
			t.Fatalf("bounds=%d,%d want %d,%d", s, e, tc.s, tc.e)
		}
	}
}

func TestBuildProjectCollaborationKeepsArtifactsOutOfAgentOverview(t *testing.T) {
	task, source, event := collaborationUUID(11), collaborationUUID(12), collaborationUUID(13)
	data := projectCollaborationDataset{
		ProjectID: "p", Runs: []db.ListProjectCollaborationRunsRow{{ID: task, AgentID: collaborationUUID(14), AgentName: "worker", Status: "completed", CreatedAt: collaborationTime()}, {ID: source, AgentID: collaborationUUID(15), AgentName: "source", Status: "completed", CreatedAt: collaborationTime()}},
		GraphEvents: []db.ListVisibleProjectGraphEventsRow{
			{ID: event, TaskID: task, CreatedAt: collaborationTime(), Data: []byte(`{"artifact":{"derived_from_task_id":"` + source.String() + `"}}`), ArtifactAttachmentIds: []byte(`[]`)},
		},
		projectCollaborationPage: projectCollaborationPage{Limit: 100},
	}
	graph := buildProjectCollaboration(data)
	for _, node := range graph.Nodes {
		if node.Type != "agent" {
			t.Fatalf("overview contains non-agent node: %+v", node)
		}
	}
	for _, edge := range graph.Edges {
		if edge.Type == "produced" || edge.Type == "derived_from" {
			t.Fatalf("artifact detail escaped into overview: %+v", edge)
		}
	}
}

func TestProjectCollaborationCountsIssuesAndRunsSeparately(t *testing.T) {
	issue, agent, parent := collaborationUUID(30), collaborationUUID(31), collaborationUUID(32)
	rows := []db.ListProjectCollaborationRunsRow{
		{ID: collaborationUUID(33), IssueID: issue, AgentID: agent, SourceAgentID: parent, RelationType: "delegated", Status: "completed", TaskActive: true},
		{ID: collaborationUUID(34), IssueID: issue, AgentID: agent, SourceAgentID: parent, RelationType: "delegated", Status: "failed", TaskActive: true},
	}
	graph := buildProjectCollaboration(projectCollaborationDataset{Runs: rows})
	if graph.Summary.TaskCount != 1 || graph.Summary.ActiveCount != 1 || graph.Summary.RunCount != 2 {
		t.Fatalf("counts=%+v", graph.Summary)
	}
	if len(graph.Edges) != 1 || graph.Edges[0].Count != 1 || graph.Edges[0].ActiveCount != 1 || graph.Edges[0].EvidenceCount != 2 {
		t.Fatalf("edges=%+v", graph.Edges)
	}
	for _, node := range graph.Nodes {
		if node.ID == "agent:"+uuidToString(agent) && (node.Status == "running" || node.Data.FailedCount != 1) {
			t.Fatalf("unfinished task is not running: %+v", node)
		}
	}
}

func TestProjectCollaborationIssueLessRunsAreNotTasks(t *testing.T) {
	graph := buildProjectCollaboration(projectCollaborationDataset{Runs: []db.ListProjectCollaborationRunsRow{{ID: collaborationUUID(50), AgentID: collaborationUUID(51), Status: "running"}}})
	if graph.Summary.TaskCount != 0 || graph.Summary.ActiveCount != 0 || graph.Summary.RunCount != 1 || graph.Nodes[0].Data.RunCount != 1 {
		t.Fatalf("issue-less counts=%+v nodes=%+v", graph.Summary, graph.Nodes)
	}
}

func TestProjectCollaborationRecoveredRunDoesNotKeepAgentFailed(t *testing.T) {
	issue, agent := collaborationUUID(60), collaborationUUID(61)
	for _, latestStatus := range []string{"completed", "failed"} {
		rows := []db.ListProjectCollaborationRunsRow{
			{ID: collaborationUUID(62), IssueID: issue, AgentID: agent, Status: "failed", CreatedAt: collaborationTime(), TaskActive: true},
			{ID: collaborationUUID(63), IssueID: issue, AgentID: agent, Status: latestStatus, CreatedAt: pgtype.Timestamptz{Time: time.Unix(20, 0), Valid: true}, TaskActive: true},
		}
		graph := buildProjectCollaboration(projectCollaborationDataset{Runs: rows})
		want := "idle"
		if latestStatus == "failed" {
			want = "failed"
		}
		if graph.Nodes[0].Status != want || graph.Nodes[0].Data.FailedCount < 1 {
			t.Fatalf("latest=%s node=%+v", latestStatus, graph.Nodes[0])
		}
	}
}
