package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMCPDiagnosticsExposeCurrentRunBindingAcrossSurfaces(t *testing.T) {
	// Given a task-owned service with complete diagnostic context.
	d := &Daemon{cfg: Config{Profile: "fixture"}, client: NewClient("https://example.test")}
	d.client.SetToken("fixture-token")
	task := Task{ID: "run-a", WorkspaceID: "workspace-a", AgentID: "agent-a", IssueID: "issue-a", ChatSessionID: "chat-a"}
	first := registerTaskManagedMCPReadiness(d, task, "custom", "http://127.0.0.1:1/private-route", "task", false)
	defer first()
	// When diagnostics are read through the real HTTP handlers.
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/readiness", d.mcpReadinessHandler())
	mux.HandleFunc("/mcp/services", d.builtinMCPDetailsHandler())
	mux.HandleFunc("/health", d.healthHandler(time.Now()))
	server := httptest.NewServer(mux)
	defer server.Close()
	for _, path := range []string{"/mcp/readiness", "/mcp/services", "/health"} {
		t.Run(path, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer fixture-token")
			request.Header.Set("X-Multica-Profile", "fixture")
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatal(response.StatusCode)
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			key := "mcp"
			if path == "/mcp/services" {
				key = "instances"
			}
			var instances []map[string]any
			if err := json.Unmarshal(body[key], &instances); err != nil {
				t.Fatal(err)
			}
			// Then every diagnostic surface identifies the run without exposing its route.
			for _, instance := range instances {
				if instance["name"] != "custom" {
					continue
				}
				for key, expected := range map[string]string{"task_id": task.ID, "workspace_id": task.WorkspaceID, "agent_id": task.AgentID, "issue_id": task.IssueID, "chat_session_id": task.ChatSessionID} {
					if instance[key] != expected {
						t.Errorf("%s = %v, want %s", key, instance[key], expected)
					}
				}
				if instance["endpoint"] != nil {
					t.Error("private endpoint exposed")
				}
				return
			}
			t.Fatal("task service missing")
		})
	}
}

func TestMCPReadinessDoesNotShareProbeOwnershipAcrossRuns(t *testing.T) {
	// Given two runs of the same agent using the same configured endpoint.
	d := &Daemon{}
	first := registerTaskManagedMCPReadiness(d, Task{ID: "run-a", WorkspaceID: "workspace"}, "custom", "http://127.0.0.1:1/mcp", "task", true)
	defer first()
	second := registerTaskManagedMCPReadiness(d, Task{ID: "run-b", WorkspaceID: "workspace"}, "custom", "http://127.0.0.1:1/mcp", "task", true)
	defer second()
	var count int
	for _, snapshot := range d.mcpReadinessSnapshot() {
		if snapshot.Name == "custom" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("concurrent run diagnostics merged: %d", count)
	}
	// When one run finishes.
	first()
	// Then the remaining entry belongs to the other run alone.
	var instances []MCPReadinessSnapshot
	for _, snapshot := range d.mcpReadinessSnapshot() {
		if snapshot.Name == "custom" {
			instances = append(instances, snapshot)
		}
	}
	if len(instances) != 1 {
		t.Fatalf("instances = %+v", instances)
	}
	encoded, err := json.Marshal(instances[0])
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["task_id"] != "run-b" {
		t.Fatalf("remaining run = %v", snapshot["task_id"])
	}
}
