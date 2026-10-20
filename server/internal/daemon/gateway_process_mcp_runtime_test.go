//go:build darwin || linux

package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
	task := Task{ID: "gateway-mcp-task", Agent: &AgentData{
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
}
