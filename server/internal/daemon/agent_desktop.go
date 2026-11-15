package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/multica-ai/multica/server/internal/agentdesktop"
)

var (
	errAgentDesktopProvider = errors.New("agent-desktop is unavailable for this provider")
	errAgentDesktopBusy     = errors.New("another run is already operating this runtime's desktop")
)

type agentDesktopExecution struct {
	once    sync.Once
	release func()
}

func (e *agentDesktopExecution) Close() {
	if e == nil {
		return
	}
	e.once.Do(e.release)
}

func (d *Daemon) startTaskAgentDesktop(ctx context.Context, task Task, provider string) (json.RawMessage, *agentDesktopExecution, error) {
	if task.MirrorSource == nil {
		return nil, nil, nil
	}
	if !providerSupportsAgentDesktop(provider) {
		return nil, nil, errAgentDesktopProvider
	}
	executable, err := agentdesktop.Resolve()
	if err != nil {
		return nil, nil, err
	}
	if err = agentdesktop.Prepare(ctx, executable); err != nil {
		return nil, nil, err
	}
	multicaExecutable, err := daemonExecutable(d.cfg.NativeHostExecutable)
	if err != nil {
		return nil, nil, err
	}
	lease, err := d.acquireAgentDesktopLease(task.RuntimeID, task.ID)
	if err != nil {
		return nil, nil, err
	}
	config, err := agentdesktop.MCPConfig(agentdesktop.BridgeConfig{
		MulticaExecutable: multicaExecutable,
		TaskID:            task.ID,
		RuntimeID:         task.RuntimeID,
		Executable:        executable,
		Headed:            d.cfg.AgentDesktopHeaded,
	})
	if err != nil {
		lease.Close()
		return nil, nil, err
	}
	return config, lease, nil
}

func daemonExecutable(configured string) (string, error) {
	if configured != "" {
		return filepath.Abs(configured)
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve multica executable: %w", err)
	}
	return filepath.Abs(executable)
}

func (d *Daemon) acquireAgentDesktopLease(runtimeID, taskID string) (*agentDesktopExecution, error) {
	if runtimeID == "" || taskID == "" {
		return nil, errors.New("agent-desktop task identity is required")
	}
	d.agentDesktopMu.Lock()
	defer d.agentDesktopMu.Unlock()
	if d.agentDesktopTasks == nil {
		d.agentDesktopTasks = make(map[string]string)
	}
	if owner := d.agentDesktopTasks[runtimeID]; owner != "" && owner != taskID {
		return nil, fmt.Errorf("%w: owner=%s", errAgentDesktopBusy, owner)
	}
	d.agentDesktopTasks[runtimeID] = taskID
	return &agentDesktopExecution{release: func() {
		d.agentDesktopMu.Lock()
		defer d.agentDesktopMu.Unlock()
		if d.agentDesktopTasks[runtimeID] == taskID {
			delete(d.agentDesktopTasks, runtimeID)
		}
	}}, nil
}

func mergeAgentDesktopMCP(base, overlay json.RawMessage) (json.RawMessage, error) {
	if len(overlay) == 0 {
		return base, nil
	}
	var document map[string]json.RawMessage
	if len(base) > 0 && string(base) != "null" {
		if err := json.Unmarshal(base, &document); err != nil {
			return nil, err
		}
	}
	for _, key := range []string{"mcpServers", "mcp"} {
		var entries map[string]json.RawMessage
		if raw := document[key]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &entries); err != nil {
				return nil, err
			}
		}
		if _, exists := entries[agentdesktop.ServerName]; exists {
			return nil, errors.New("reserved MCP name agent-desktop already configured")
		}
	}
	return mergeTaskRemoteMCPConfig(base, overlay)
}

func providerSupportsAgentDesktop(provider string) bool {
	switch provider {
	case "claude", "codebuddy", "codearts", "codex", "cursor", "grok",
		"hermes", "kimi", "reasonix", "dsh", "kiro", "opencode", "openclaw",
		"pi", "qoder", "qoderclicn", "qwen", "qwenpaw", "mcode", "devin",
		"traecli", "dim", "omp", "zcode":
		return true
	default:
		return false
	}
}

const agentDesktopInstructions = "\nFor desktop work, use the agent-desktop MCP tools. Run desktop_skills_get with name \"desktop\" before the first action and with name \"platform\" for the current OS. Operate only through agent-desktop's snapshot/find/action/verify loop. Refs from desktop_snapshot go stale when the UI changes; take a fresh snapshot instead of repeating an uncertain action. Retry an action only when its error disposition says retry is safe. Do not use shell commands or another desktop automation implementation as a GUI fallback. Trace and screenshots stay local unless the user explicitly asks to attach them."

func strictJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid trailing JSON")
	}
	return nil
}
