package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestMCPReadinessProbesInitializeAndToolsList(t *testing.T) {
	var methods []string
	var methodsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		methodsMu.Lock()
		methods = append(methods, request["method"].(string))
		methodsMu.Unlock()
		if request["method"] == "tools/list" && r.Header.Get("Mcp-Session-Id") != "session-1" {
			t.Errorf("tools/list missing initialize session header: %q", r.Header.Get("Mcp-Session-Id"))
		}
		w.Header().Set("Content-Type", "application/json")
		if request["method"] == "initialize" {
			w.Header().Set("Mcp-Session-Id", "session-1")
		}
		if request["method"] == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		id := request["id"]
		result := map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}}
		if request["method"] == "initialize" {
			result["result"] = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}}
		} else {
			result["result"] = map[string]any{"tools": []interface{}{map[string]interface{}{"name": "one"}, map[string]interface{}{"name": "two"}}}
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()

	d := &Daemon{cfg: Config{Profile: "profile-a"}}
	cleanup := registerTaskManagedMCPReadiness(d, "workspace-a", "managed", server.URL, "task", true)
	defer cleanup()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshots := d.mcpReadinessSnapshot()
		for _, snapshot := range snapshots {
			if snapshot.Name == "managed" && snapshot.State == MCPReadinessReady {
				if snapshot.ToolCount != 2 || !snapshot.Ready {
					t.Fatalf("unexpected snapshot: %+v", snapshot)
				}
				methodsMu.Lock()
				gotMethods := append([]string(nil), methods...)
				methodsMu.Unlock()
				if len(gotMethods) != 3 || gotMethods[0] != "initialize" || gotMethods[1] != "notifications/initialized" || gotMethods[2] != "tools/list" {
					t.Fatalf("probe methods = %#v", gotMethods)
				}
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("probe did not become ready: %+v", d.mcpReadinessSnapshot())
}

func TestMCPReadinessDoesNotExecuteTools(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request["method"] == "tools/call" {
			called = true
		}
		w.Header().Set("Content-Type", "application/json")
		result := map[string]any{"tools": []any{}}
		if request["method"] == "initialize" {
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := probeMCPInitializeAndTools(ctx, server.Client(), server.URL); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("readiness probe called an MCP tool")
	}
}

func TestMCPReadinessClassifiesMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer server.Close()
	d := &Daemon{cfg: Config{Profile: "profile-a"}}
	cleanup := registerTaskManagedMCPReadiness(d, "workspace-a", "broken", server.URL, "task", true)
	defer cleanup()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshots := d.mcpReadinessSnapshot()
		for _, snapshot := range snapshots {
			if snapshot.Name == "broken" && snapshot.State == MCPReadinessProtocolError {
				if snapshot.Ready || snapshot.Reason != "invalid_mcp_response" {
					t.Fatalf("unexpected snapshot: %+v", snapshot)
				}
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("probe did not classify malformed response: %+v", d.mcpReadinessSnapshot())
}

func TestMCPReadinessDefaultsAreNotReady(t *testing.T) {
	d := &Daemon{cfg: Config{LLM2JevEnabled: true}}
	statuses := healthMCPStatuses(d.mcpReadinessSnapshot())
	if len(statuses) != 2 {
		t.Fatalf("statuses = %+v", statuses)
	}
	for _, status := range statuses {
		if status.Ready || status.State != MCPReadinessNotConfigured || status.Reason != "task_not_started" {
			t.Fatalf("built-in MCP falsely ready: %+v", status)
		}
	}
}

func TestMCPReadinessDefaultsRemainVisibleWithCustomEntries(t *testing.T) {
	d := &Daemon{cfg: Config{LLM2JevEnabled: true}}
	cleanup := registerTaskManagedMCPReadiness(d, "workspace-a", "custom", "", "task", false)
	defer cleanup()
	statuses := healthMCPStatuses(d.mcpReadinessSnapshot())
	seen := make(map[string]HealthMCPStatus, len(statuses))
	for _, status := range statuses {
		seen[status.Name] = status
	}
	for _, name := range []string{llm2jevMCPName, identityActionsMCPName, "custom"} {
		status, ok := seen[name]
		if !ok {
			t.Fatalf("missing MCP status %q: %+v", name, statuses)
		}
		if name != "custom" && (status.Ready || status.State != MCPReadinessNotConfigured) {
			t.Fatalf("built-in MCP falsely ready: %+v", status)
		}
	}
}

func TestMCPReadinessDoesNotProbeExternalEndpoint(t *testing.T) {
	d := &Daemon{cfg: Config{Profile: "profile-a"}}
	config := json.RawMessage(`{"mcpServers":{"external":{"type":"http","url":"https://example.invalid/mcp"}}}`)
	cleanup := registerTaskMCPReadinessFromConfig(d, "workspace-a", config, "task")
	defer cleanup()
	statuses := d.mcpReadinessSnapshot()
	var external *MCPReadinessSnapshot
	for i := range statuses {
		if statuses[i].Name == "external" {
			external = &statuses[i]
			break
		}
	}
	if external == nil || external.Ready || external.Reason != "non_local_endpoint" {
		t.Fatalf("external endpoint should be withheld from probe: %+v", statuses)
	}
}
