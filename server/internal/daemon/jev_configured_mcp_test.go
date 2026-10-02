package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

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
