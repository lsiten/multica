package agentdesktop

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestApprovalForToolCallRequiresEveryHighRiskBatchCommand(t *testing.T) {
	tests := []struct {
		name      string
		tool      string
		arguments string
		want      []string
	}{
		{
			name:      "observation stays direct",
			tool:      "desktop_snapshot",
			arguments: `{"app":"Notes"}`,
			want:      nil,
		},
		{
			name:      "raw pointer needs approval",
			tool:      "desktop_mouse_down",
			arguments: `{"x":10,"y":20}`,
			want:      []string{"desktop_mouse_down"},
		},
		{
			name:      "read-only permissions stay direct",
			tool:      "desktop_permissions",
			arguments: `{}`,
			want:      nil,
		},
		{
			name:      "permission prompt needs approval",
			tool:      "desktop_permissions",
			arguments: `{"request":true}`,
			want:      []string{"desktop_permissions"},
		},
		{
			name:      "batch surfaces high-risk child",
			tool:      "desktop_batch",
			arguments: `{"commands_json":"[{\"command\":\"snapshot\"},{\"command\":\"clipboard-set\",\"args\":{\"text\":\"secret\"}}]"}`,
			want:      []string{"desktop_clipboard_set"},
		},
		{
			name:      "batch cannot hide behind malformed input",
			tool:      "desktop_batch",
			arguments: `{"commands_json":"not-json"}`,
			want:      []string{"desktop_batch (unvalidated)"},
		},
		{
			name:      "safe batch stays direct",
			tool:      "desktop_batch",
			arguments: `{"commands_json":"[{\"command\":\"snapshot\"},{\"command\":\"wait\",\"args\":{\"ms\":100}}]"}`,
			want:      nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := approvalForToolCall(tt.tool, json.RawMessage(tt.arguments))
			if !reflect.DeepEqual(got.Commands, tt.want) {
				t.Fatalf("commands = %v, want %v", got.Commands, tt.want)
			}
		})
	}
}

func TestApprovalDecisionBindsApprovalToTheExactRevision(t *testing.T) {
	request := approvalRequestView{Status: "answered", Response: json.RawMessage(`{"revision":2,"decision":"approve"}`)}
	if _, err := approvalDecision(request, 1); err == nil {
		t.Fatal("approval for a different revision was accepted")
	}
	if _, err := approvalDecision(request, 2); err != nil {
		t.Fatalf("current revision rejected: %v", err)
	}
}
