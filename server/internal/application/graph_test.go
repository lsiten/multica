package application

import (
	"reflect"
	"testing"
)

func graphNodes() []Node {
	return []Node{
		{ID: "shop", WorkspaceID: "ws", ProjectID: "project", Kind: "composition", Revision: 1},
		{ID: "ui", WorkspaceID: "ws", ProjectID: "project", Kind: "composition", Revision: 1},
		{ID: "web", WorkspaceID: "ws", ProjectID: "project", Kind: "service", Revision: 2},
		{ID: "admin", WorkspaceID: "ws", ProjectID: "project", Kind: "service", Revision: 1},
		{ID: "api", WorkspaceID: "ws", ProjectID: "project", Kind: "service", Revision: 3},
		{ID: "worker", WorkspaceID: "ws", ProjectID: "project", Kind: "service", Revision: 1},
	}
}

func TestPlanNestedCompositionDeduplicatesAndOrdersServices(t *testing.T) {
	relations := []Relation{
		{SourceID: "shop", TargetID: "ui", Type: "contains", Required: true},
		{SourceID: "shop", TargetID: "api", Type: "contains", Required: true},
		{SourceID: "ui", TargetID: "web", Type: "contains", Required: true},
		{SourceID: "ui", TargetID: "admin", Type: "contains", Required: false},
		{SourceID: "shop", TargetID: "web", Type: "contains", Required: true},
		{SourceID: "ui", TargetID: "api", Type: "depends_on", Condition: "healthy"},
		{SourceID: "worker", TargetID: "api", Type: "related"},
	}
	plan, err := BuildPlan("shop", graphNodes(), relations)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Waves, [][]string{{"api"}, {"admin", "web"}}) {
		t.Fatalf("unexpected waves: %+v", plan)
	}
	if len(plan.Nodes) != 3 || plan.Nodes[0].Required || plan.Nodes[1].Revision != 3 || !plan.Nodes[2].Required {
		t.Fatalf("membership, required flags or revision snapshot incorrect: %+v", plan.Nodes)
	}
}

func TestPlanExternalDependencyChecksUnlessExplicitlyStarted(t *testing.T) {
	dependency := Relation{SourceID: "web", TargetID: "api", Type: "depends_on", Condition: "healthy"}
	plan, err := BuildPlan("web", graphNodes(), []Relation{dependency})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 1 || len(plan.Nodes[0].Dependencies) != 1 || plan.Nodes[0].Dependencies[0].ID != "api" {
		t.Fatalf("external dependency was started or lost: %+v", plan)
	}
	dependency.StartExternal = true
	plan, err = BuildPlan("web", graphNodes(), []Relation{dependency})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Waves, [][]string{{"api"}, {"web"}}) {
		t.Fatalf("explicit startup: %+v", plan)
	}
}

func TestApplicationGraphRejectsInvalidRelationships(t *testing.T) {
	cases := []struct {
		name      string
		relations []Relation
		edit      func([]Node)
	}{
		{"dependency cycle", []Relation{{SourceID: "web", TargetID: "api", Type: "depends_on", Condition: "healthy"}, {SourceID: "api", TargetID: "web", Type: "depends_on", Condition: "started"}}, nil},
		{"composition cycle", []Relation{{SourceID: "shop", TargetID: "ui", Type: "contains"}, {SourceID: "ui", TargetID: "shop", Type: "contains"}}, nil},
		{"service contains", []Relation{{SourceID: "web", TargetID: "api", Type: "contains"}}, nil},
		{"missing target", []Relation{{SourceID: "web", TargetID: "absent", Type: "related"}}, nil},
		{"self relation", []Relation{{SourceID: "web", TargetID: "web", Type: "related"}}, nil},
		{"cross project", []Relation{{SourceID: "web", TargetID: "api", Type: "related"}}, func(nodes []Node) { nodes[4].ProjectID = "other" }},
		{"cross workspace", []Relation{{SourceID: "web", TargetID: "api", Type: "related"}}, func(nodes []Node) { nodes[4].WorkspaceID = "other" }},
		{"related controls execution", []Relation{{SourceID: "web", TargetID: "api", Type: "related", StartExternal: true}}, nil},
		{"duplicate related", []Relation{{SourceID: "web", TargetID: "api", Type: "related"}, {SourceID: "api", TargetID: "web", Type: "related"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodes := graphNodes()
			if tc.edit != nil {
				tc.edit(nodes)
			}
			if err := ValidateGraph(nodes, tc.relations); err == nil {
				t.Fatal("invalid relationship graph accepted")
			}
		})
	}
}

func TestPlanRejectsCycleCreatedByNestedMembership(t *testing.T) {
	relations := []Relation{
		{SourceID: "shop", TargetID: "web", Type: "contains", Required: true},
		{SourceID: "shop", TargetID: "admin", Type: "contains", Required: true},
		{SourceID: "web", TargetID: "ui", Type: "depends_on", Condition: "healthy"},
		{SourceID: "ui", TargetID: "admin", Type: "contains", Required: true},
		{SourceID: "admin", TargetID: "web", Type: "depends_on", Condition: "healthy"},
	}
	if _, err := BuildPlan("shop", graphNodes(), relations); err == nil {
		t.Fatal("expanded cycle accepted")
	}
}
