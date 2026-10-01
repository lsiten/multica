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
func TestBuildProjectCollaborationDeduplicatesEventsAndSkipsSelfEdges(t *testing.T) {
	parent, child := collaborationUUID(1), collaborationUUID(2)
	rows := []db.ListProjectCollaborationRunsRow{
		{ID: collaborationUUID(3), AgentID: parent, AgentName: "lead", Status: "completed", CreatedAt: collaborationTime(), RelationType: "root"},
		{ID: collaborationUUID(4), AgentID: child, AgentName: "worker", Status: "running", CreatedAt: collaborationTime(), SourceTaskID: parent, SourceAgentID: parent, SourceAgentName: "lead", RelationType: "delegated"},
		{ID: collaborationUUID(5), AgentID: child, AgentName: "worker", Status: "running", CreatedAt: collaborationTime(), SourceTaskID: parent, SourceAgentID: parent, SourceAgentName: "lead", RelationType: "delegated"},
		{ID: collaborationUUID(6), AgentID: child, AgentName: "worker", Status: "running", CreatedAt: collaborationTime(), SourceTaskID: child, SourceAgentID: child, RelationType: "retry"},
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

func TestBuildProjectCollaborationAddsProvenArtifactDependencies(t *testing.T) {
	task, source, event := collaborationUUID(11), collaborationUUID(12), collaborationUUID(13)
	data := projectCollaborationDataset{
		ProjectID: "p", Runs: []db.ListProjectCollaborationRunsRow{{ID: task, AgentID: collaborationUUID(14), AgentName: "worker", Status: "completed", CreatedAt: collaborationTime()}, {ID: source, AgentID: collaborationUUID(15), AgentName: "source", Status: "completed", CreatedAt: collaborationTime()}},
		GraphEvents: []db.ListVisibleProjectGraphEventsRow{
			{ID: event, TaskID: task, CreatedAt: collaborationTime(), Data: []byte(`{"artifact":{"derived_from_task_id":"` + source.String() + `"}}`), ArtifactAttachmentIds: []byte(`[]`)},
		},
		projectCollaborationPage: projectCollaborationPage{Limit: 100},
	}
	graph := buildProjectCollaboration(data)
	foundProduced, foundDerived := false, false
	for _, edge := range graph.Edges {
		foundProduced = foundProduced || edge.Type == "produced"
		foundDerived = foundDerived || edge.Type == "derived_from"
	}
	if !foundProduced || !foundDerived {
		t.Fatalf("artifact edges=%+v", graph.Edges)
	}
	for _, node := range graph.Nodes {
		if node.Type == "artifact" && node.ID != "artifact:"+event.String() {
			t.Fatalf("unexpected artifact node=%+v", node)
		}
	}
}
