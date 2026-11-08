package daemon

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/jevmodels"
	"github.com/multica-ai/multica/server/internal/modelservice"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestStartTaskConfiguredJevMCPRejectsTypedNilBackend(t *testing.T) {
	var backend *modelservice.Client
	d := &Daemon{
		jevModels:       backend,
		jevModelsErr:    errors.New("model backend unavailable"),
		jevModelRetryAt: time.Now().Add(time.Hour),
	}
	task := Task{
		ID:        "typed-nil-jev-task",
		JevConfig: &protocol.WorkspaceJevConfig{Source: "local", ModelID: "fixture", TimeoutSeconds: 45},
		Agent:     &AgentData{Model: "fixture"},
	}
	if _, _, err := d.startTaskConfiguredJevMCP(context.Background(), task, "claude", nil); err == nil {
		t.Fatal("typed-nil Jev backend must fail closed instead of panicking")
	}
}

func TestStartTaskConfiguredJevMCPSourceMatrix(t *testing.T) {
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()

	tests := []struct {
		name       string
		config     *protocol.WorkspaceJevConfig
		customEnv  map[string]string
		wantConfig bool
		wantError  bool
	}{
		{name: "agent context without provider", config: &protocol.WorkspaceJevConfig{Source: "agent_context", TimeoutSeconds: 45}, customEnv: map[string]string{}, wantConfig: false},
		{name: "remote provider", config: &protocol.WorkspaceJevConfig{Source: "remote", Endpoint: remote.URL, ModelID: "fixture", CredentialEnv: "JEV_API_KEY", TimeoutSeconds: 45}, customEnv: map[string]string{"JEV_API_KEY": "secret"}, wantConfig: true},
		{name: "remote credential missing", config: &protocol.WorkspaceJevConfig{Source: "remote", Endpoint: remote.URL, ModelID: "fixture", CredentialEnv: "JEV_API_KEY", TimeoutSeconds: 45}, customEnv: map[string]string{}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d := &Daemon{cfg: Config{LLM2JevMaxConcurrency: 1, LLM2JevTimeout: 2 * time.Second}}
			task := Task{ID: "jev-matrix-task", JevConfig: test.config, Agent: &AgentData{Model: "fixture", CustomEnv: test.customEnv}}
			config, set, err := d.startTaskConfiguredJevMCP(context.Background(), task, "claude", nil)
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v wantError=%v", err, test.wantError)
			}
			if set != nil {
				defer set.Close()
			}
			if (len(config) > 0) != test.wantConfig {
				t.Fatalf("config=%q wantConfig=%v", config, test.wantConfig)
			}
			if test.wantConfig && !strings.Contains(string(config), llm2jevMCPName) {
				t.Fatalf("config missing %s: %s", llm2jevMCPName, config)
			}
		})
	}
}

// TestStartTaskConfiguredJevMCPFailsClosedOnInvalidContext locks GAP-06: a
// broken F2 boundary must fail closed (error, no started MCP set) instead of
// only logging a warning and handing the illegal context to the AI service. A
// zero concurrency/timeout yields MaxConcurrent/MaxCalls/CallTimeout of zero in
// the derived EvaluationContext, so Validate() rejects it.
func TestStartTaskConfiguredJevMCPFailsClosedOnInvalidContext(t *testing.T) {
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()

	d := &Daemon{cfg: Config{LLM2JevMaxConcurrency: 0, LLM2JevTimeout: 0}}
	task := Task{
		ID:        "jev-fail-closed-task",
		JevConfig: &protocol.WorkspaceJevConfig{Source: "remote", Endpoint: remote.URL, ModelID: "fixture", CredentialEnv: "JEV_API_KEY", TimeoutSeconds: 45},
		Agent:     &AgentData{Model: "fixture", CustomEnv: map[string]string{"JEV_API_KEY": "secret"}},
	}
	_, set, err := d.startTaskConfiguredJevMCP(context.Background(), task, "claude", nil)
	if err == nil {
		t.Fatal("invalid Jev evaluation context must fail closed")
	}
	if set != nil {
		t.Fatal("invalid Jev evaluation context must not start an MCP set")
	}
}

type configuredJevTransport func(*http.Request) (*http.Response, error)

func (f configuredJevTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestConfiguredJevUsesSelectedCredentialAndTimeout(t *testing.T) {
	for _, tc := range []struct{ name, credentialEnv, authorization string }{
		{"anonymous remote", "", ""},
		{"selected remote credential", "JEV_KEY", "Bearer selected-fixture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Daemon{cfg: Config{LLM2JevMaxConcurrency: 1, LLM2JevTimeout: 2 * time.Second}}
			task := Task{
				ID:    "configured-wire",
				Agent: &AgentData{CustomEnv: map[string]string{"OPENAI_API_KEY": "agent-fixture", "JEV_KEY": "selected-fixture"}},
				JevConfig: &protocol.WorkspaceJevConfig{
					Source: "remote", Endpoint: "https://jev.example.test", ModelID: "fixture",
					CredentialEnv: tc.credentialEnv, TimeoutSeconds: 17,
				},
			}
			originalEnv := maps.Clone(task.Agent.CustomEnv)
			config, set, err := d.startTaskConfiguredJevMCP(context.Background(), task, "claude", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			server := set.(*configuredJevMCPSet).inner.(*llm2jevMCPSet).server.Handler.(*llm2jevMCPServer)
			observed := make(chan struct {
				auth    string
				timeout time.Duration
			}, 1)
			server.client.Transport = configuredJevTransport(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok {
					return nil, errors.New("missing evaluation deadline")
				}
				observed <- struct {
					auth    string
					timeout time.Duration
				}{r.Header.Get("Authorization"), time.Until(deadline)}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"answers":{"route":{"type":"choice","choice":"yes","probabilities":{"yes":0.99,"no":0.01}}}}`))}, nil
			})
			result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{
				"name": jevMCPToolName,
				"arguments": map[string]any{
					"state": map[string]any{"text": "fixture"},
					"questions": map[string]any{
						"route": map[string]any{"type": "choice", "instructions": "route", "criteria": map[string]any{"yes": "yes", "no": "no"}},
					},
				},
			})
			if result["isError"] == true {
				t.Fatalf("decision failed: %v", result)
			}
			got := <-observed
			if got.auth != tc.authorization {
				t.Errorf("authorization=%q want=%q", got.auth, tc.authorization)
			}
			if got.timeout < 16*time.Second || got.timeout > 17*time.Second {
				t.Errorf("outbound deadline=%s want configured 17s", got.timeout)
			}
			if !maps.Equal(task.Agent.CustomEnv, originalEnv) || task.Agent.Model != "" {
				t.Fatal("original agent configuration mutated")
			}
			ec := d.buildEvaluationContext(task, "claude", task.JevConfig, task.JevConfig.Endpoint, "")
			if ec.CallTimeout != 17*time.Second {
				t.Errorf("evaluation context timeout=%s want 17s", ec.CallTimeout)
			}
			t.Logf("configured MCP decision succeeded, auth matches=%v, outbound deadline=%s, evaluation timeout=%s", got.auth == tc.authorization, got.timeout, ec.CallTimeout)
		})
	}
}

type configuredJevBackend struct{ modelservice.Backend }

func (configuredJevBackend) Acquire(context.Context, jevmodels.Selection, modelservice.Execution) (*modelservice.Lease, error) {
	return &modelservice.Lease{Endpoint: "http://127.0.0.1:1", Token: "local-fixture"}, nil
}

func TestConfiguredJevUnsupportedProviderReadinessUsesResolvedConfig(t *testing.T) {
	for _, source := range []string{"remote", "local", "agent_context"} {
		t.Run(source, func(t *testing.T) {
			d := &Daemon{cfg: Config{LLM2JevMaxConcurrency: 1, LLM2JevTimeout: time.Second}, jevModels: configuredJevBackend{}}
			task := Task{ID: "configured-readiness", Agent: &AgentData{}, JevConfig: &protocol.WorkspaceJevConfig{Source: source, ModelID: "fixture", TimeoutSeconds: 17}}
			switch source {
			case "remote":
				task.JevConfig.Endpoint = "https://jev.example.test"
			case "local":
				task.JevConfig.ModelID = "fixture/model"
				task.JevConfig.ModelRevision = strings.Repeat("a", 40)
				task.JevConfig.Device = "cpu"
			case "agent_context":
				task.JevConfig.ModelID = ""
			}
			_, set, err := d.startTaskConfiguredJevMCP(context.Background(), task, "unsupported-fixture", nil)
			if err != nil {
				t.Fatal(err)
			}
			if set != nil {
				defer set.Close()
			}
			count := 0
			for _, state := range d.mcpReadinessSnapshot() {
				if state.TaskID == task.ID && state.Name == llm2jevMCPName && state.State == MCPReadinessProviderUnavailable {
					count++
				}
			}
			want := 1
			if source == "agent_context" {
				want = 0
			}
			if count != want {
				t.Fatalf("provider_unavailable readiness entries=%d want=%d for source %s", count, want, source)
			}
			if set != nil {
				set.Close()
			}
			for _, state := range d.mcpReadinessSnapshot() {
				if state.TaskID == task.ID {
					t.Fatalf("task readiness retained after close: %+v", state)
				}
			}
			t.Logf("source=%s unavailable entries=%d, cleaned on close", source, count)
		})
	}
}
