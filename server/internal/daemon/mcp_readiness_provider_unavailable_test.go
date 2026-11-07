package daemon

import (
	"testing"
)

// GAP-05: when a fully configured task's provider cannot advertise the decision
// MCP, the decision tool is silently not started and the user is not told. These
// tests pin the reason classification and the readiness entry that replaces the
// generic not_configured / task_not_started fallback.
func TestLLM2JevMCPUnavailableReason(t *testing.T) {
	configured := Task{
		ID:          "task-a",
		WorkspaceID: "workspace-a",
		AgentID:     "agent-a",
		Agent:       &AgentData{Name: "Codex", Model: "gpt-5.1", CustomEnv: map[string]string{"OPENAI_API_KEY": "secret"}},
	}
	// The decision MCP is intentionally absent only when the agent is fully
	// configured but the provider cannot advertise it (GAP-05).
	if reason := llm2jevMCPUnavailableReason(configured, "openclaw"); reason != "provider_unavailable" {
		t.Fatalf("unsupported provider: got %q, want provider_unavailable", reason)
	}
	// A supported provider starts the MCP, so there is no unavailability gap.
	if reason := llm2jevMCPUnavailableReason(configured, "codex"); reason != "" {
		t.Fatalf("supported provider: got %q, want empty", reason)
	}
	// Missing agent / env / model is a legitimate no-start, not the gap.
	if reason := llm2jevMCPUnavailableReason(Task{Agent: nil}, "openclaw"); reason != "" {
		t.Fatalf("no agent: got %q, want empty", reason)
	}
	if reason := llm2jevMCPUnavailableReason(Task{Agent: &AgentData{Model: "gpt-5.1"}}, "openclaw"); reason != "" {
		t.Fatalf("no custom env: got %q, want empty", reason)
	}
	if reason := llm2jevMCPUnavailableReason(Task{Agent: &AgentData{CustomEnv: map[string]string{"OPENAI_API_KEY": "secret"}}}, "openclaw"); reason != "" {
		t.Fatalf("no model: got %q, want empty", reason)
	}
}

func TestMCPReadinessProviderUnavailableSurfacesInsteadOfNotStarted(t *testing.T) {
	d := &Daemon{cfg: Config{LLM2JevEnabled: true}}
	task := Task{
		ID:          "task-b",
		WorkspaceID: "workspace-b",
		AgentID:     "agent-b",
		Agent:       &AgentData{Name: "Codex", Model: "gpt-5.1", CustomEnv: map[string]string{"OPENAI_API_KEY": "secret"}},
	}
	// Given a fully configured task whose provider cannot advertise the decision MCP.
	cleanup := registerTaskMCPReadinessProviderUnavailable(d, task, llm2jevMCPName, "task", llm2jevMCPUnavailableReason(task, "openclaw"))
	defer cleanup()

	// Then the snapshot reports provider_unavailable instead of the generic not_configured fallback.
	entries := d.mcpReadinessSnapshot()
	var found bool
	for _, entry := range entries {
		if entry.Name != llm2jevMCPName {
			continue
		}
		found = true
		if entry.State != MCPReadinessProviderUnavailable || entry.Reason != "provider_unavailable" {
			t.Fatalf("decision MCP state=%s reason=%q", entry.State, entry.Reason)
		}
		if entry.Ready {
			t.Fatalf("provider_unavailable must not be ready")
		}
		if !entry.Enabled {
			t.Fatalf("provider_unavailable must stay enabled")
		}
	}
	if !found {
		t.Fatalf("expected a decision MCP readiness entry")
	}
}
