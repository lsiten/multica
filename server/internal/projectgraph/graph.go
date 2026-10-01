package projectgraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	NodeProject   = "project"
	NodeTask      = "task"
	NodeAgent     = "agent"
	NodeArtifact  = "artifact"
	NodeDecision  = "decision"
	EdgeDependsOn = "depends_on"
	EdgeProduced  = "produced_by"
	EdgeDerived   = "derived_from"
	EdgeAssigned  = "assigned_to"
	EdgeReviewed  = "reviewed_by"
)

var (
	ErrInvalidNode = errors.New("project graph: invalid node")
	ErrInvalidEdge = errors.New("project graph: invalid edge")
)

type Node struct {
	ID        string
	ProjectID string
	Type      string
	Title     string
	Data      map[string]any
}

type Edge struct {
	From string
	To   string
	Type string
}

type Graph struct {
	ProjectID string
	Nodes     map[string]Node
	Edges     []Edge
}

func New(projectID string) (Graph, error) {
	if strings.TrimSpace(projectID) == "" {
		return Graph{}, fmt.Errorf("%w: project_id is required", ErrInvalidNode)
	}
	return Graph{ProjectID: projectID, Nodes: map[string]Node{}}, nil
}

func (g *Graph) AddNode(node Node) error {
	if g == nil || strings.TrimSpace(g.ProjectID) == "" || strings.TrimSpace(node.ID) == "" || node.ProjectID != g.ProjectID || !validNodeType(node.Type) {
		return ErrInvalidNode
	}
	if g.Nodes == nil {
		g.Nodes = map[string]Node{}
	}
	if _, exists := g.Nodes[node.ID]; exists {
		return fmt.Errorf("%w: duplicate node %q", ErrInvalidNode, node.ID)
	}
	g.Nodes[node.ID] = node
	return nil
}

func (g *Graph) AddEdge(edge Edge) error {
	if g == nil || edge.From == "" || edge.To == "" || edge.From == edge.To || !validEdgeType(edge.Type) {
		return ErrInvalidEdge
	}
	if _, ok := g.Nodes[edge.From]; !ok {
		return fmt.Errorf("%w: source node %q is missing", ErrInvalidEdge, edge.From)
	}
	if _, ok := g.Nodes[edge.To]; !ok {
		return fmt.Errorf("%w: target node %q is missing", ErrInvalidEdge, edge.To)
	}
	for _, existing := range g.Edges {
		if existing == edge {
			return fmt.Errorf("%w: duplicate edge", ErrInvalidEdge)
		}
	}
	g.Edges = append(g.Edges, edge)
	return nil
}

func (g Graph) MarshalJSON() ([]byte, error) {
	if strings.TrimSpace(g.ProjectID) == "" {
		return nil, ErrInvalidNode
	}
	return json.Marshal(map[string]any{
		"project_id": g.ProjectID,
		"nodes":      g.Nodes,
		"edges":      g.Edges,
	})
}

func validNodeType(value string) bool {
	switch value {
	case NodeProject, NodeTask, NodeAgent, NodeArtifact, NodeDecision:
		return true
	default:
		return false
	}
}

func validEdgeType(value string) bool {
	switch value {
	case EdgeDependsOn, EdgeProduced, EdgeDerived, EdgeAssigned, EdgeReviewed:
		return true
	default:
		return false
	}
}
