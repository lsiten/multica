package projectgraph

import (
	"errors"
	"testing"
)

func TestGraphBuildsValidatedTaskArtifactDecisionLineage(t *testing.T) {
	graph, err := New("project-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range []Node{
		{ID: "task-1", ProjectID: "project-1", Type: NodeTask, Title: "Run"},
		{ID: "artifact-1", ProjectID: "project-1", Type: NodeArtifact, Title: "Report"},
		{ID: "decision-1", ProjectID: "project-1", Type: NodeDecision, Title: "Verified"},
	} {
		if err := graph.AddNode(node); err != nil {
			t.Fatal(err)
		}
	}
	for _, edge := range []Edge{
		{From: "artifact-1", To: "task-1", Type: EdgeProduced},
		{From: "decision-1", To: "artifact-1", Type: EdgeReviewed},
	} {
		if err := graph.AddEdge(edge); err != nil {
			t.Fatal(err)
		}
	}
	if len(graph.Nodes) != 3 || len(graph.Edges) != 2 {
		t.Fatalf("graph=%+v", graph)
	}
}

func TestGraphRejectsCrossProjectAndDuplicateRelations(t *testing.T) {
	graph, err := New("project-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AddNode(Node{ID: "foreign", ProjectID: "project-2", Type: NodeTask}); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("cross-project node error=%v", err)
	}
	if err := graph.AddNode(Node{ID: "task", ProjectID: "project-1", Type: NodeTask}); err != nil {
		t.Fatal(err)
	}
	edge := Edge{From: "task", To: "task", Type: EdgeDependsOn}
	if err := graph.AddEdge(edge); !errors.Is(err, ErrInvalidEdge) {
		t.Fatalf("self edge error=%v", err)
	}
}
