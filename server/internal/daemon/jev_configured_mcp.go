package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"

	"github.com/multica-ai/multica/server/internal/jevmodels"
	"github.com/multica-ai/multica/server/internal/modelservice"
	"time"
)

type configuredJevMCPSet struct {
	inner  interface{ Close() }
	lease  *modelservice.Lease
	logger *slog.Logger
}

func (s *configuredJevMCPSet) Close() {
	if s == nil {
		return
	}
	if s.inner != nil {
		s.inner.Close()
	}
	if s.lease != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.lease.Release(ctx); err != nil && s.logger != nil {
			s.logger.Warn("model lease release unconfirmed", "error", err)
		}
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
	var lease *modelservice.Lease
	switch cfg.Source {
	case "local":
		manager, err := d.jevModelManager()
		if err != nil {
			return nil, nil, err
		}
		lease, err = manager.Acquire(ctx, jevmodels.Selection{ModelID: cfg.ModelID, Revision: cfg.ModelRevision, Device: cfg.Device}, modelservice.Execution{WorkspaceID: task.WorkspaceID, RuntimeID: task.RuntimeID, TaskID: task.ID, DispatchedAt: task.DispatchedAt})
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
	// F2 boundary: build the minimum typed EvaluationContext the task owner
	// hands to the AI service and validate it. The raw agent environment,
	// CustomEnv, lease and PAT stay in their own owners; this is only the
	// narrow evaluation description. Enforcement (failing on an invalid
	// context) lands with the AI service in F3; until then a broken context
	// is logged so the boundary is exercised without gating the hot path.
	evalCtx := d.buildEvaluationContext(effective, provider, &cfg, effective.Agent.CustomEnv["OPENAI_BASE_URL"], effective.Agent.CustomEnv["OPENAI_API_KEY"])
	if err := evalCtx.Validate(); err != nil && logger != nil {
		logger.Warn("invalid Jev evaluation context", "task", task.ID, "error", err)
	}

	config, set, err := startTaskLLM2JevMCPAtWithLimitsAndBroker(ctx, task.ID, provider, effective, logger, "127.0.0.1", "", d.cfg.LLM2JevMaxConcurrency, d.cfg.LLM2JevTimeout, int64(d.cfg.LLM2JevMaxConcurrency)*32, d.builtinMCP)
	if err != nil {
		if lease != nil {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 10*time.Second)
			releaseErr := lease.Release(releaseCtx)
			releaseCancel()
			if releaseErr != nil && logger != nil {
				logger.Warn("model lease release unconfirmed", "error", releaseErr)
			}
		}
		return nil, nil, err
	}
	return config, &configuredJevMCPSet{inner: set, lease: lease, logger: logger}, nil
}

func configuredJevSource(cfg *protocol.WorkspaceJevConfig) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Source)
}
