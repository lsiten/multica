package daemonws

import (
	"context"
	"encoding/json"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Hub) StartAgentDesktopControl(
	ctx context.Context,
	workspaceID, runtimeID, daemonID, controlID, name string,
) (protocol.AgentDesktopControlResult, error) {
	query := protocol.AgentDesktopControlStartQuery{
		ControlID: controlID,
		Name:      name,
	}
	return h.agentDesktopControlQuery(ctx, workspaceID, runtimeID, daemonID,
		protocol.EventAgentDesktopControlStartQuery,
		protocol.EventAgentDesktopControlStartResult,
		30*time.Second,
		func(envelope protocol.RuntimeEnvelope) (json.RawMessage, error) {
			query.RuntimeEnvelope = envelope
			return json.Marshal(query)
		},
	)
}

func (h *Hub) SendAgentDesktopControlInput(
	ctx context.Context,
	workspaceID, runtimeID, daemonID, controlID string,
	input protocol.AgentDesktopControlInput,
) (protocol.AgentDesktopControlResult, error) {
	query := protocol.AgentDesktopControlInputQuery{
		ControlID: controlID,
		Input:     input,
	}
	return h.agentDesktopControlQuery(ctx, workspaceID, runtimeID, daemonID,
		protocol.EventAgentDesktopControlInputQuery,
		protocol.EventAgentDesktopControlInputResult,
		8*time.Second,
		func(envelope protocol.RuntimeEnvelope) (json.RawMessage, error) {
			query.RuntimeEnvelope = envelope
			return json.Marshal(query)
		},
	)
}

func (h *Hub) StopAgentDesktopControl(
	ctx context.Context,
	workspaceID, runtimeID, daemonID, controlID string,
) (protocol.AgentDesktopControlResult, error) {
	query := protocol.AgentDesktopControlStopQuery{ControlID: controlID}
	return h.agentDesktopControlQuery(ctx, workspaceID, runtimeID, daemonID,
		protocol.EventAgentDesktopControlStopQuery,
		protocol.EventAgentDesktopControlStopResult,
		15*time.Second,
		func(envelope protocol.RuntimeEnvelope) (json.RawMessage, error) {
			query.RuntimeEnvelope = envelope
			return json.Marshal(query)
		},
	)
}

func (h *Hub) agentDesktopControlQuery(
	ctx context.Context,
	workspaceID, runtimeID, daemonID string,
	event, resultEvent string,
	timeout time.Duration,
	payload func(protocol.RuntimeEnvelope) (json.RawMessage, error),
) (protocol.AgentDesktopControlResult, error) {
	raw, err := h.queryRuntime(ctx, runtimeQuery{
		workspaceID: workspaceID,
		runtimeID:   runtimeID,
		daemonID:    daemonID,
		event:       event,
		resultEvent: resultEvent,
		timeout:     timeout,
		payload:     payload,
	})
	if err != nil {
		return protocol.AgentDesktopControlResult{}, err
	}
	var result protocol.AgentDesktopControlResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return protocol.AgentDesktopControlResult{}, err
	}
	if err := result.Validate(); err != nil {
		return protocol.AgentDesktopControlResult{}, err
	}
	return result, nil
}
