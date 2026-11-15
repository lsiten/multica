package daemonws

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type runtimeQueryPending struct {
	client    *client
	envelope  protocol.RuntimeEnvelope
	event     string
	result    chan json.RawMessage
	expiresAt time.Time
}

type runtimeQuery struct {
	workspaceID string
	runtimeID   string
	daemonID    string
	event       string
	resultEvent string
	timeout     time.Duration
	payload     func(protocol.RuntimeEnvelope) (json.RawMessage, error)
}

func (h *Hub) pruneRuntimeQueriesLocked(now time.Time) {
	for id, pending := range h.runtimeQueriesPending {
		if now.After(pending.expiresAt) {
			delete(h.runtimeQueriesPending, id)
		}
	}
}

func (h *Hub) queryRuntime(ctx context.Context, query runtimeQuery) (json.RawMessage, error) {
	timeout := query.timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	h.mu.Lock()
	h.pruneRuntimeQueriesLocked(time.Now())
	client := h.mirrorClientLocked(query.workspaceID, query.runtimeID, query.daemonID)
	if client == nil {
		h.mu.Unlock()
		return nil, ErrMirrorUnavailable
	}
	if len(h.runtimeQueriesPending) >= 1024 {
		h.mu.Unlock()
		return nil, ErrMirrorCapacity
	}
	envelope := protocol.RuntimeEnvelope{
		WorkspaceID:      query.workspaceID,
		RuntimeID:        query.runtimeID,
		DaemonGeneration: client.vscreenGeneration,
		RequestID:        uuid.NewString(),
	}
	payload, err := query.payload(envelope)
	if err != nil {
		h.mu.Unlock()
		return nil, err
	}
	pending := &runtimeQueryPending{
		client:    client,
		envelope:  envelope,
		event:     query.resultEvent,
		result:    make(chan json.RawMessage, 1),
		expiresAt: time.Now().Add(timeout),
	}
	if h.runtimeQueriesPending == nil {
		h.runtimeQueriesPending = make(map[string]*runtimeQueryPending)
	}
	h.runtimeQueriesPending[envelope.RequestID] = pending
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.runtimeQueriesPending, envelope.RequestID)
		h.mu.Unlock()
	}()
	frame := protocol.Message{Type: query.event, Payload: payload}
	if !client.trySend(mustMarshalRaw(frame)) {
		return nil, ErrMirrorUnavailable
	}
	select {
	case result := <-pending.result:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-client.ctx.Done():
		return nil, ErrMirrorUnavailable
	}
}

func (c *client) handleRuntimeQueryResult(raw json.RawMessage, event string) {
	if len(raw) > 256*1024 {
		return
	}
	var envelope protocol.RuntimeEnvelope
	if json.Unmarshal(raw, &envelope) != nil {
		return
	}
	h := c.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	pending := h.runtimeQueriesPending[envelope.RequestID]
	if pending == nil || pending.client != c || pending.event != event ||
		pending.envelope != envelope || time.Now().After(pending.expiresAt) ||
		h.mirrorClientLocked(envelope.WorkspaceID, envelope.RuntimeID, c.identity.DaemonID) != c {
		return
	}
	select {
	case pending.result <- raw:
	default:
	}
}
