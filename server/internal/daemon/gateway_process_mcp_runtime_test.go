//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGatewayProcessTaskMCPHandshake(t *testing.T) {
	// Given a real gateway child and a path-bound task MCP handler.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" || r.Header.Get("Authorization") != "Bearer fixture-pat" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"11111111-1111-4111-8111-111111111111"}`)
	}))
	defer backend.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	d := New(Config{
		ProcessServices: []string{"gateway"}, ServerBaseURL: backend.URL,
		DaemonID: "owned-daemon", Profile: "owned-fixture",
		WorkspacesRoot:       filepath.Join(root, "workspaces"),
		NativeHostExecutable: executable, NativeHostBuild: "fixture/commit",
		NativeVscreenPreferencesPath: filepath.Join(root, "vscreen-enabled.json"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetToken("fixture-pat")
	gateway, err := d.ensureGatewayProcess(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.close)
	if !gateway.ready() {
		t.Fatal("gateway must be ready before the task handshake")
	}
	task := Task{ID: "gateway-mcp-task", WorkspaceID: "workspace-a", AgentID: "agent-a", IssueID: "issue-a", Agent: &AgentData{
		Model: "fixture-model", CustomEnv: map[string]string{"OPENAI_BASE_URL": "http://127.0.0.1:1"},
	}}
	config, set, err := startTaskLLM2JevMCPAtWithLimitsAndBroker(t.Context(), task.ID, "codex", task, nil,
		"127.0.0.1", "", llm2jevMCPMaxConcurrent, llm2jevMCPCallTimeout, llm2jevMCPMaxCalls, gateway)
	if err != nil || set == nil {
		t.Fatalf("start task MCP: set=%v err=%v", set, err)
	}
	t.Cleanup(set.Close)
	endpoint := llm2jevMCPURL(t, config)

	// When an agent initializes and lists tools through the gateway listener.
	ctx, cancel := context.WithTimeout(t.Context(), mcpReadinessProbeTimeout)
	defer cancel()
	count, err := probeMCPInitializeAndTools(ctx, http.DefaultClient, endpoint)

	// Then the task route serves all tools without contacting the model provider.
	if err != nil || count != 3 {
		t.Fatalf("gateway ready but task MCP handshake failed: tools=%d err=%v", count, err)
	}

	// The profile diagnostic must attribute the successful handshake to this run.
	cleanup := registerTaskMCPReadinessFromConfig(d, task, config, "task")
	t.Cleanup(cleanup)
	server := httptest.NewServer(d.mcpReadinessHandler())
	defer server.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/mcp/readiness", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer fixture-pat")
	request.Header.Set("X-Multica-Profile", d.cfg.Profile)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			MCP []HealthMCPStatus `json:"mcp"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&body)
		response.Body.Close()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		for _, entry := range body.MCP {
			if entry.TaskID == task.ID && entry.Name == llm2jevMCPName && entry.Ready {
				if entry.AgentID != task.AgentID || entry.IssueID != task.IssueID || entry.ToolCount != 3 {
					t.Fatalf("wrong current run: %+v", entry)
				}
				return
			}
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("current run never became ready: %+v", body)
		}
	}
}
