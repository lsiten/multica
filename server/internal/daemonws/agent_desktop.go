package daemonws

import (
	"context"
	"encoding/json"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Hub) QueryAgentDesktopReadiness(ctx context.Context, workspaceID, runtimeID, daemonID string) (protocol.AgentDesktopReadiness, error) {
	raw, err := h.queryRuntime(ctx, runtimeQuery{
		workspaceID: workspaceID,
		runtimeID:   runtimeID,
		daemonID:    daemonID,
		event:       protocol.EventAgentDesktopReadinessQuery,
		resultEvent: protocol.EventAgentDesktopReadinessResult,
		payload: func(envelope protocol.RuntimeEnvelope) (json.RawMessage, error) {
			return json.Marshal(protocol.AgentDesktopReadinessQuery{RuntimeEnvelope: envelope})
		},
	})
	if err != nil {
		return protocol.AgentDesktopReadiness{}, err
	}
	var result protocol.AgentDesktopReadinessResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return protocol.AgentDesktopReadiness{}, err
	}
	if err := result.Validate(); err != nil {
		return protocol.AgentDesktopReadiness{}, err
	}
	return result.Readiness, nil
}

func (h *Hub) OpenAgentDesktopPermissionGuide(ctx context.Context, workspaceID, runtimeID, daemonID string) (protocol.AgentDesktopReadiness, error) {
	raw, err := h.queryRuntime(ctx, runtimeQuery{
		workspaceID: workspaceID,
		runtimeID:   runtimeID,
		daemonID:    daemonID,
		event:       protocol.EventAgentDesktopPermissionGuideQuery,
		resultEvent: protocol.EventAgentDesktopPermissionGuideResult,
		payload: func(envelope protocol.RuntimeEnvelope) (json.RawMessage, error) {
			return json.Marshal(protocol.AgentDesktopPermissionGuideQuery{RuntimeEnvelope: envelope})
		},
	})
	if err != nil {
		return protocol.AgentDesktopReadiness{}, err
	}
	var result protocol.AgentDesktopPermissionGuideResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return protocol.AgentDesktopReadiness{}, err
	}
	if err := result.Validate(); err != nil {
		return protocol.AgentDesktopReadiness{}, err
	}
	return result.Readiness, nil
}
