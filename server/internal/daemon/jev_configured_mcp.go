package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"

	"github.com/multica-ai/multica/server/internal/jevmodels"
)

type configuredJevMCPSet struct {
	inner interface{ Close() }
	lease *jevmodels.Lease
}

func (s *configuredJevMCPSet) Close() {
	if s == nil {
		return
	}
	if s.inner != nil {
		s.inner.Close()
	}
	if s.lease != nil {
		s.lease.Release()
	}
}

func (d *Daemon) startTaskConfiguredJevMCP(ctx context.Context, task Task, provider string, logger *slog.Logger) (json.RawMessage, interface{ Close() }, error) {
	if task.JevConfig == nil || task.Agent == nil {
		return nil, nil, nil
	}
	cfg := *task.JevConfig
	if err := cfg.Validate(); err != nil {
		return nil, nil, fmt.Errorf("invalid workspace Jev config: %w", err)
	}
	effective := task
	agentCopy := *task.Agent
	env := map[string]string{}
	for k, v := range task.Agent.CustomEnv {
		env[k] = v
	}
	agentCopy.CustomEnv = env
	effective.Agent = &agentCopy
	var lease *jevmodels.Lease
	switch cfg.Source {
	case "local":
		manager, err := d.jevModelManager()
		if err != nil {
			return nil, nil, err
		}
		lease, err = manager.Acquire(ctx, jevmodels.Selection{ModelID: cfg.ModelID, Revision: cfg.ModelRevision, Device: cfg.Device})
		if err != nil {
			return nil, nil, fmt.Errorf("acquire local Jev model: %w", err)
		}
		effective.Agent.CustomEnv["OPENAI_BASE_URL"] = lease.Endpoint
		effective.Agent.CustomEnv["OPENAI_API_KEY"] = lease.Token
		effective.Agent.CustomEnv["MULTICA_JEV_SYSTEMONE"] = "1"
		effective.Agent.Model = cfg.ModelID
	case "remote":
		effective.Agent.CustomEnv["OPENAI_BASE_URL"] = cfg.Endpoint
		if cfg.CredentialEnv != "" {
			credential := strings.TrimSpace(task.Agent.CustomEnv[cfg.CredentialEnv])
			if credential == "" {
				return nil, nil, fmt.Errorf("remote Jev credential environment %q is not configured", cfg.CredentialEnv)
			}
			effective.Agent.CustomEnv["OPENAI_API_KEY"] = credential
		}
		effective.Agent.CustomEnv["MULTICA_JEV_SYSTEMONE"] = "1"
		effective.Agent.Model = cfg.ModelID
	case "agent_context":
		// The current-context source uses the existing Agent model adapter; no
		// independent model is started. Its semantic result remains uncalibrated.
	default:
		return nil, nil, fmt.Errorf("unsupported Jev source %q", cfg.Source)
	}
	config, set, err := startTaskLLM2JevMCPAtWithLimitsAndBroker(ctx, task.ID, provider, effective, logger, "127.0.0.1", "", d.cfg.LLM2JevMaxConcurrency, d.cfg.LLM2JevTimeout, int64(d.cfg.LLM2JevMaxConcurrency)*32, d.builtinMCP)
	if err != nil {
		if lease != nil {
			lease.Release()
		}
		return nil, nil, err
	}
	return config, &configuredJevMCPSet{inner: set, lease: lease}, nil
}

func configuredJevSource(cfg *protocol.WorkspaceJevConfig) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Source)
}
