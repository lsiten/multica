package daemonws

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

var (
	ErrMirrorUnavailable = errors.New("mirror: daemon unavailable")
	ErrMirrorCapacity    = errors.New("mirror: request capacity reached")
)

func (h *Hub) mirrorClientLocked(workspaceID, runtimeID, daemonID string) *client {
	var newest *client
	for c := range h.byRuntime[runtimeID] {
		if c.identity.DaemonID == daemonID && c.identity.AllowsWorkspace(workspaceID) &&
			c.allowsRuntime(runtimeID) && (newest == nil || c.registeredAt.After(newest.registeredAt)) {
			newest = c
		}
	}
	return newest
}

// QueryMirrorSources waits at most five seconds for this runtime's current
// daemon to report its authorized read-only display sources.
func (h *Hub) QueryMirrorSources(ctx context.Context, workspaceID, runtimeID, daemonID string) (protocol.MirrorSourcesResult, error) {
	raw, err := h.queryRuntime(ctx, runtimeQuery{
		workspaceID: workspaceID,
		runtimeID:   runtimeID,
		daemonID:    daemonID,
		event:       protocol.EventMirrorSourcesQuery,
		resultEvent: protocol.EventMirrorSourcesResult,
		payload: func(envelope protocol.RuntimeEnvelope) (json.RawMessage, error) {
			return json.Marshal(protocol.MirrorSourcesQuery{RuntimeEnvelope: envelope})
		},
	})
	if err != nil {
		return protocol.MirrorSourcesResult{}, err
	}
	var result protocol.MirrorSourcesResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return protocol.MirrorSourcesResult{}, err
	}
	if err := result.Validate(); err != nil {
		return protocol.MirrorSourcesResult{}, err
	}
	return result, nil
}
