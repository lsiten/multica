package service

import (
	"context"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestResolveTaskWorkspaceIDProjectCoordinationContext(t *testing.T) {
	for _, tc := range []struct {
		name    string
		context string
		want    string
	}{
		{"project coordination", `{"type":"project_supervision","workspace_id":"workspace-owned","project_id":"project-owned"}`, "workspace-owned"},
		{"human followup", `{"type":"human_request_followup","workspace_id":"workspace-owned","source_task_id":"source-task"}`, "workspace-owned"},
		{"human followup missing workspace", `{"type":"human_request_followup","source_task_id":"source-task"}`, ""},
		{"malformed human followup", `{"type":"human_request_followup",`, ""},
		{"unknown context cannot grant access", `{"type":"unknown","workspace_id":"workspace-owned"}`, ""},
		{"missing workspace", `{"type":"project_supervision","project_id":"project-owned"}`, ""},
		{"malformed context", `{"type":"project_supervision",`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace, err := (&TaskService{}).ResolveTaskWorkspaceIDChecked(context.Background(), db.AgentTaskQueue{Context: []byte(tc.context)})
			if err != nil || workspace != tc.want {
				t.Fatalf("workspace=%q error=%v, want %q", workspace, err, tc.want)
			}
		})
	}
}
