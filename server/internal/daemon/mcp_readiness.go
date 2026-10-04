package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// MCPReadinessState describes the last bounded protocol probe of a managed MCP
// server. A server is only marked ready after initialize and tools/list both
// complete successfully; configuration alone is never readiness.
type MCPReadinessState string

const (
	MCPReadinessNotConfigured       MCPReadinessState = "not_configured"
	MCPReadinessBrokerReady         MCPReadinessState = "broker_ready"
	MCPReadinessProbing             MCPReadinessState = "probing"
	MCPReadinessReady               MCPReadinessState = "ready"
	MCPReadinessProviderUnavailable MCPReadinessState = "provider_unavailable"
	MCPReadinessCapabilityRequired  MCPReadinessState = "capability_required"
	MCPReadinessOffline             MCPReadinessState = "offline"
	MCPReadinessTimeout             MCPReadinessState = "timeout"
	MCPReadinessProtocolError       MCPReadinessState = "protocol_error"
)

// MCPReadinessSnapshot is intentionally diagnostic only. It omits endpoint,
// path tokens, bearer tokens, environment variables and tool schemas.
type MCPReadinessSnapshot struct {
	InstanceID  string            `json:"instance_id,omitempty"`
	Name        string            `json:"name"`
	WorkspaceID string            `json:"workspace_id,omitempty"`
	Enabled     bool              `json:"enabled"`
	Scope       string            `json:"scope"`
	Ready       bool              `json:"ready"`
	State       MCPReadinessState `json:"state"`
	Reason      string            `json:"reason,omitempty"`
	ToolCount   int               `json:"tool_count,omitempty"`
	CheckedAt   string            `json:"checked_at,omitempty"`
}

type mcpReadinessEntry struct {
	MCPReadinessSnapshot
	endpoint string
	mu       sync.RWMutex
	owners   int
	cancel   context.CancelFunc
	done     chan struct{}
}

type mcpReadinessRegistry struct {
	mu      sync.RWMutex
	entries map[string]*mcpReadinessEntry
	nextID  uint64
}

// The registry is keyed by daemon pointer so readiness cannot be read across
// profiles. Entries are task scoped and are removed by the returned cleanup
// function when the task-owned MCP server exits.
var mcpReadinessRegistries sync.Map // map[*Daemon]*mcpReadinessRegistry

func (d *Daemon) mcpReadinessRegistry() *mcpReadinessRegistry {
	if value, ok := mcpReadinessRegistries.Load(d); ok {
		return value.(*mcpReadinessRegistry)
	}
	created := &mcpReadinessRegistry{entries: make(map[string]*mcpReadinessEntry)}
	actual, _ := mcpReadinessRegistries.LoadOrStore(d, created)
	return actual.(*mcpReadinessRegistry)
}

// registerTaskManagedMCPReadiness registers an MCP URL produced by the daemon
// for one task. The endpoint is kept process-local and never returned in a
// health response. The probe only sends initialize/tools/list; it never calls
// an MCP tool or executes a command.
func registerTaskManagedMCPReadiness(d *Daemon, workspaceID, name, endpoint, scope string, enabled bool) func() {
	name = strings.TrimSpace(name)
	endpoint = strings.TrimSpace(endpoint)
	if d == nil || name == "" {
		return func() {}
	}
	if scope == "" {
		scope = "task"
	}
	entry := &mcpReadinessEntry{MCPReadinessSnapshot: MCPReadinessSnapshot{
		Name: name, WorkspaceID: strings.TrimSpace(workspaceID), Enabled: enabled, Scope: scope,
		Ready: false, State: MCPReadinessNotConfigured,
	}, endpoint: endpoint}
	if endpoint != "" && enabled && !isLocalMCPProbeEndpoint(endpoint) {
		entry.Reason = "non_local_endpoint"
	}
	if endpoint == "" || !enabled {
		if !enabled {
			entry.State = MCPReadinessNotConfigured
			entry.Reason = "disabled"
		} else {
			entry.Reason = "endpoint_missing"
		}
	}
	registry := d.mcpReadinessRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	entry.owners, entry.cancel, entry.done = 1, cancel, make(chan struct{})
	registry.mu.Lock()
	// Built-in registration and the merged task config can reference the same
	// route. Share its probe while retaining each caller's cleanup ownership.
	if endpoint != "" {
		for _, registered := range registry.entries {
			if registered.Name == name && registered.WorkspaceID == entry.WorkspaceID && registered.Scope == scope && registered.Enabled == enabled && registered.endpoint == endpoint {
				registered.owners++
				registry.mu.Unlock()
				cancel()
				return registry.release(registered)
			}
		}
	}
	registry.nextID++
	entry.InstanceID = fmt.Sprintf("mcp-%d", registry.nextID)
	registry.entries[entry.InstanceID] = entry
	registry.mu.Unlock()
	if endpoint != "" && enabled && isLocalMCPProbeEndpoint(endpoint) {
		go func() {
			defer close(entry.done)
			probeMCPReadiness(ctx, entry)
		}()
	} else {
		close(entry.done)
	}
	return registry.release(entry)
}

func (registry *mcpReadinessRegistry) release(entry *mcpReadinessEntry) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			registry.mu.Lock()
			entry.owners--
			lastOwner := entry.owners == 0
			if lastOwner {
				delete(registry.entries, entry.InstanceID)
			}
			registry.mu.Unlock()
			if lastOwner {
				entry.cancel()
				<-entry.done
			}
		})
	}
}

func (d *Daemon) mcpReadinessSnapshot() []MCPReadinessSnapshot {
	registry := d.mcpReadinessRegistry()
	registry.mu.RLock()
	entries := make([]*mcpReadinessEntry, 0, len(registry.entries))
	for _, entry := range registry.entries {
		entries = append(entries, entry)
	}
	registry.mu.RUnlock()
	out := make([]MCPReadinessSnapshot, 0, len(entries)+2)
	if d.builtinMCP != nil && d.builtinMCP.ready() {
		out = append(out,
			MCPReadinessSnapshot{Name: llm2jevMCPName, InstanceID: "builtin-broker", Enabled: true, Scope: "daemon", Ready: true, State: MCPReadinessBrokerReady, Reason: "broker_listening"},
			MCPReadinessSnapshot{Name: identityActionsMCPName, InstanceID: "builtin-broker", Enabled: true, Scope: "daemon", Ready: true, State: MCPReadinessBrokerReady, Reason: "broker_listening"},
		)
	}
	for _, entry := range entries {
		entry.mu.RLock()
		snapshot := entry.MCPReadinessSnapshot
		entry.mu.RUnlock()
		out = append(out, snapshot)
	}
	// A current daemon reports the daemon-scoped broker entries above. Keep the
	// legacy task_not_started fallback only for older fixtures/daemons that do
	// not have a broker yet; this preserves response compatibility while the
	// desktop update rolls out.
	names := make(map[string]struct{}, len(out))
	for _, snapshot := range out {
		names[snapshot.Name] = struct{}{}
	}
	if _, ok := names[llm2jevMCPName]; !ok {
		out = append(out, MCPReadinessSnapshot{Name: llm2jevMCPName, Enabled: d.cfg.LLM2JevEnabled, Scope: "task", State: MCPReadinessNotConfigured, Reason: "task_not_started"})
	}
	if _, ok := names[identityActionsMCPName]; !ok {
		out = append(out, MCPReadinessSnapshot{Name: identityActionsMCPName, Enabled: true, Scope: "task", State: MCPReadinessNotConfigured, Reason: "task_not_started"})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].InstanceID < out[j].InstanceID
	})
	return out
}

// registerTaskMCPReadinessFromConfig discovers only URL-based MCP entries in a
// task overlay. STDIO entries are intentionally reported as not configured
// until their owning runtime performs its own handshake; this daemon health
// probe never launches arbitrary commands.
func registerTaskMCPReadinessFromConfig(d *Daemon, workspaceID string, config json.RawMessage, scope string) func() {
	var document struct {
		MCPServers map[string]struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"mcpServers"`
	}
	if len(config) == 0 || json.Unmarshal(config, &document) != nil {
		return func() {}
	}
	cleanups := make([]func(), 0, len(document.MCPServers))
	for name, server := range document.MCPServers {
		endpoint := strings.TrimSpace(server.URL)
		if endpoint == "" {
			continue
		}

		cleanups = append(cleanups, registerTaskManagedMCPReadiness(d, workspaceID, name, endpoint, scope, true))
	}
	return func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}
}

func isLocalMCPProbeEndpoint(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() == "" {
		return false
	}
	if u.Scheme != "http" {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}
