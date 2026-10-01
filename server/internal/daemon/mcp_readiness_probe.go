package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const mcpReadinessProbeTimeout = 3 * time.Second

func probeMCPReadiness(parent context.Context, entry *mcpReadinessEntry) {
	entry.mu.Lock()
	entry.State = MCPReadinessProbing
	entry.Reason = ""
	entry.Ready = false
	entry.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, mcpReadinessProbeTimeout)
	defer cancel()
	client := &http.Client{Timeout: mcpReadinessProbeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var toolCount int
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		toolCount, err = probeMCPInitializeAndTools(ctx, client, entry.endpoint)
		if err == nil || errors.Is(err, errMCPProtocol) || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err == nil {
		entry.State, entry.Ready, entry.ToolCount, entry.Reason = MCPReadinessReady, true, toolCount, ""
		return
	}
	entry.Ready = false
	entry.ToolCount = 0
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		entry.State, entry.Reason = MCPReadinessTimeout, "probe_timeout"
	case errors.Is(err, errMCPProtocol):
		entry.State, entry.Reason = MCPReadinessProtocolError, "invalid_mcp_response"
	default:
		entry.State, entry.Reason = MCPReadinessOffline, "endpoint_unreachable"
	}
}

var errMCPProtocol = errors.New("invalid MCP response")

type mcpJSONRPCResponse struct {
	JSONRPC   string          `json:"jsonrpc"`
	ID        json.RawMessage `json:"id"`
	Result    json.RawMessage `json:"result"`
	Error     json.RawMessage `json:"error"`
	SessionID string          `json:"-"`
}

func probeMCPInitializeAndTools(ctx context.Context, client *http.Client, endpoint string) (int, error) {
	if endpoint == "" {
		return 0, fmt.Errorf("%w: empty endpoint", errMCPProtocol)
	}
	initialize := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05", "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "multica-daemon-readiness", "version": "1"},
		},
	}
	initializeResponse, err := mcpReadinessRPC(ctx, client, endpoint, initialize, true, "")
	if err != nil {
		return 0, err
	}
	var initialized struct {
		ProtocolVersion string                     `json:"protocolVersion"`
		Capabilities    map[string]json.RawMessage `json:"capabilities"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(initializeResponse.Result, &initialized); err != nil || initialized.ProtocolVersion != "2024-11-05" {
		return 0, fmt.Errorf("%w: initialize result", errMCPProtocol)
	}
	if initialized.Capabilities == nil {
		return 0, fmt.Errorf("%w: capabilities missing", errMCPProtocol)
	}
	toolsCapability, ok := initialized.Capabilities["tools"]
	trimmedToolsCapability := bytes.TrimSpace(toolsCapability)
	if !ok || len(trimmedToolsCapability) == 0 || bytes.Equal(trimmedToolsCapability, []byte("null")) || bytes.Equal(trimmedToolsCapability, []byte("true")) || bytes.Equal(trimmedToolsCapability, []byte("false")) {
		return 0, fmt.Errorf("%w: tools capability missing or invalid", errMCPProtocol)
	}
	sessionID := initializeResponse.SessionID
	if _, err := mcpReadinessRPC(ctx, client, endpoint, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{}}, false, sessionID); err != nil {
		return 0, err
	}
	response, err := mcpReadinessRPC(ctx, client, endpoint, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}}, true, sessionID)
	if err != nil {
		return 0, err
	}
	var result struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil || result.Tools == nil {
		return 0, fmt.Errorf("%w: tools/list result", errMCPProtocol)
	}
	return len(result.Tools), nil
}
