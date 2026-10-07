// Package application owns application relationships and executable composition plans.
package application

import (
	"errors"
	"fmt"
	"sort"
)

// Node is the immutable identity and scope needed to validate a relationship graph.
type Node struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Kind        string `json:"kind"`
	Revision    int64  `json:"revision"`
}

// Relation separates composition ownership, startup prerequisites and informational links.
type Relation struct {
	SourceID      string `json:"source_id"`
	TargetID      string `json:"target_id"`
	Type          string `json:"type"`
	Required      bool   `json:"required"`
	Condition     string `json:"condition"`
	StartExternal bool   `json:"start_external"`
}

// Dependency selects an exact service and the readiness condition it must satisfy.
type Dependency struct {
	ID        string `json:"id"`
	Condition string `json:"condition"`
}

// PlanNode appears once even when multiple nested compositions reference it.
type PlanNode struct {
	ID           string       `json:"id"`
	Revision     int64        `json:"revision"`
	Required     bool         `json:"required"`
	Dependencies []Dependency `json:"dependencies"`
}

// Plan freezes composition membership and deterministic parallel startup waves.
type Plan struct {
	RootID string     `json:"root_id"`
	Nodes  []PlanNode `json:"nodes"`
	Waves  [][]string `json:"waves"`
}

// ValidateGraph rejects cross-project edges and cycles before persisting any relationships.
func ValidateGraph(nodes []Node, relations []Relation) error {
	byID := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		if node.ID == "" || node.WorkspaceID == "" || node.ProjectID == "" || (node.Kind != "service" && node.Kind != "composition") {
			return errors.New("invalid application graph node")
		}
		if _, duplicate := byID[node.ID]; duplicate {
			return errors.New("duplicate application graph node")
		}
		byID[node.ID] = node
	}
	adjacent := make(map[string][]string)
	seen := make(map[string]bool)
	for _, relation := range relations {
		source, sourceOK := byID[relation.SourceID]
		target, targetOK := byID[relation.TargetID]
		if !sourceOK || !targetOK || source.ID == target.ID {
			return errors.New("application relationship requires two existing applications")
		}
		if source.WorkspaceID != target.WorkspaceID || source.ProjectID != target.ProjectID {
			return errors.New("application relationships must stay within one project")
		}
		key := relation.SourceID + "/" + relation.Type + "/" + relation.TargetID
		if relation.Type == "related" && relation.SourceID > relation.TargetID {
			key = relation.TargetID + "/related/" + relation.SourceID
		}
		if seen[key] {
			return errors.New("duplicate application relationship")
		}
		seen[key] = true
		switch relation.Type {
		case "contains":
			if source.Kind != "composition" || relation.StartExternal || relation.Condition != "" {
				return errors.New("only compositions may contain applications")
			}
		case "depends_on":
			if relation.Condition != "healthy" && relation.Condition != "started" {
				return errors.New("dependency condition must be healthy or started")
			}
		case "related":
			if relation.StartExternal || relation.Condition != "" {
				return errors.New("informational relationships cannot control execution")
			}
			continue
		default:
			return errors.New("unknown application relationship type")
		}
		adjacent[source.ID] = append(adjacent[source.ID], target.ID)
	}
	visiting, visited := make(map[string]bool), make(map[string]bool)
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return errors.New("application composition or dependency cycle")
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, next := range adjacent[id] {
			if err := visit(next); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for id := range byID {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// BuildPlan expands nested members and explicitly opted-in external dependencies.
// Dependencies outside the plan remain checks; they do not gain process control.
func BuildPlan(rootID string, nodes []Node, relations []Relation) (Plan, error) {
	plan := Plan{RootID: rootID, Nodes: []PlanNode{}, Waves: [][]string{}}
	if err := ValidateGraph(nodes, relations); err != nil {
		return plan, err
	}
	byID, outgoing := make(map[string]Node), make(map[string][]Relation)
	for _, node := range nodes {
		byID[node.ID] = node
	}
	if _, ok := byID[rootID]; !ok {
		return plan, errors.New("application not found")
	}
	for _, relation := range relations {
		outgoing[relation.SourceID] = append(outgoing[relation.SourceID], relation)
	}
	var leaves func(string) []string
	leafCache := make(map[string][]string)
	leaves = func(id string) []string {
		if cached, ok := leafCache[id]; ok {
			return cached
		}
		if byID[id].Kind == "service" {
			leafCache[id] = []string{id}
			return leafCache[id]
		}
		unique := make(map[string]bool)
		for _, relation := range outgoing[id] {
			if relation.Type == "contains" {
				for _, leaf := range leaves(relation.TargetID) {
					unique[leaf] = true
				}
			}
		}
		leafCache[id] = sortedKeys(unique)
		return leafCache[id]
	}
	selected := make(map[string]bool)
	visited := make(map[string]int)
	var expand func(string, bool) error
	expand = func(id string, required bool) error {
		level := 1
		if required {
			level = 2
		}
		if visited[id] >= level {
			return nil
		}
		visited[id] = level
		if byID[id].Kind == "service" {
			selected[id] = selected[id] || required
		}
		for _, relation := range outgoing[id] {
			if relation.Type == "contains" || (relation.Type == "depends_on" && relation.StartExternal) {
				childRequired := required
				if relation.Type == "contains" {
					childRequired = required && relation.Required
				}
				if err := expand(relation.TargetID, childRequired); err != nil {
					return err
				}
			}
		}
		if byID[id].Kind == "composition" && len(leaves(id)) == 0 {
			return fmt.Errorf("composition %s has no services", id)
		}
		return nil
	}
	if err := expand(rootID, true); err != nil {
		return plan, err
	}
	dependencies := make(map[string]map[string]string)
	for _, relation := range relations {
		if relation.Type != "depends_on" {
			continue
		}
		for _, source := range leaves(relation.SourceID) {
			if _, ok := selected[source]; !ok {
				continue
			}
			if dependencies[source] == nil {
				dependencies[source] = make(map[string]string)
			}
			for _, target := range leaves(relation.TargetID) {
				if source == target {
					return plan, errors.New("expanded application depends on itself")
				}
				if dependencies[source][target] != "healthy" {
					dependencies[source][target] = relation.Condition
				}
			}
		}
	}
	ids := sortedKeys(selected)
	for _, id := range ids {
		node := PlanNode{ID: id, Revision: byID[id].Revision, Required: selected[id], Dependencies: []Dependency{}}
		for _, target := range sortedKeys(dependencies[id]) {
			node.Dependencies = append(node.Dependencies, Dependency{ID: target, Condition: dependencies[id][target]})
		}
		plan.Nodes = append(plan.Nodes, node)
	}
	done := make(map[string]bool)
	for len(done) < len(selected) {
		wave := []string{}
		for _, id := range ids {
			if done[id] {
				continue
			}
			ready := true
			for target := range dependencies[id] {
				if _, internal := selected[target]; internal && !done[target] {
					ready = false
					break
				}
			}
			if ready {
				wave = append(wave, id)
			}
		}
		if len(wave) == 0 {
			return plan, errors.New("expanded application dependency cycle")
		}
		plan.Waves = append(plan.Waves, wave)
		for _, id := range wave {
			done[id] = true
		}
	}
	return plan, nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
