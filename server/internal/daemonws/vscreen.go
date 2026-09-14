package daemonws

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var (
	ErrVscreenUnavailable = errors.New("vscreen: daemon unavailable")
	ErrVscreenStale       = errors.New("vscreen: stale daemon generation")
	ErrVscreenCapacity    = errors.New("vscreen: request capacity reached")
	ErrVscreenReplay      = errors.New("vscreen: command already submitted")
	ErrVscreenNotFound    = errors.New("vscreen: command not found")
)

type vscreenPending struct {
	client   *client
	envelope protocol.VscreenEnvelope
	kind     string
	result   chan protocol.VscreenQueryResult
	expires  time.Time
	receipt  protocol.VscreenCommandReceipt
	userID   string
}

func (h *Hub) vscreenClientLocked(workspaceID, runtimeID, daemonID string) *client {
	var newest *client
	for c := range h.byRuntime[runtimeID] {
		if c.identity.DaemonID == daemonID && c.identity.AllowsWorkspace(workspaceID) && c.allowsRuntime(runtimeID) && (newest == nil || c.registeredAt.After(newest.registeredAt)) {
			newest = c
		}
	}
	return newest
}

func (h *Hub) pruneVscreenLocked(now time.Time) {
	for id, p := range h.vscreenPending {
		if now.After(p.expires) {
			delete(h.vscreenPending, id)
		}
	}
}

// QueryVscreen waits at most five seconds and accepts a result only from the selected socket.
func (h *Hub) QueryVscreen(ctx context.Context, workspaceID, runtimeID, daemonID, kind string) (protocol.VscreenQueryResult, error) {
	if kind != "state" && kind != "sources" {
		return protocol.VscreenQueryResult{}, protocol.ErrInvalidVscreenContract
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	h.mu.Lock()
	h.pruneVscreenLocked(time.Now())
	c := h.vscreenClientLocked(workspaceID, runtimeID, daemonID)
	if c == nil {
		h.mu.Unlock()
		return protocol.VscreenQueryResult{}, ErrVscreenUnavailable
	}
	if len(h.vscreenPending) >= 1024 {
		h.mu.Unlock()
		return protocol.VscreenQueryResult{}, ErrVscreenCapacity
	}
	envelope := protocol.VscreenEnvelope{WorkspaceID: workspaceID, RuntimeID: runtimeID, DaemonGeneration: c.vscreenGeneration, RequestID: uuid.NewString()}
	p := &vscreenPending{client: c, envelope: envelope, kind: kind, result: make(chan protocol.VscreenQueryResult, 1), expires: time.Now().Add(5 * time.Second)}
	if h.vscreenPending == nil {
		h.vscreenPending = make(map[string]*vscreenPending)
	}
	h.vscreenPending[envelope.RequestID] = p
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.vscreenPending, envelope.RequestID); h.mu.Unlock() }()
	if !c.trySend(mustMarshalRaw(protocol.Message{Type: protocol.EventVscreenQuery, Payload: mustMarshalRaw(protocol.VscreenQuery{VscreenEnvelope: envelope, Kind: kind})})) {
		return protocol.VscreenQueryResult{}, ErrVscreenUnavailable
	}
	select {
	case result := <-p.result:
		return result, nil
	case <-ctx.Done():
		return protocol.VscreenQueryResult{}, ctx.Err()
	case <-c.ctx.Done():
		return protocol.VscreenQueryResult{}, ErrVscreenUnavailable
	}
}

func (c *client) handleVscreenQueryResult(raw json.RawMessage) {
	if len(raw) > 128*1024 {
		return
	}
	var result protocol.VscreenQueryResult
	if json.Unmarshal(raw, &result) != nil {
		return
	}
	h := c.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.vscreenPending[result.RequestID]
	if p == nil || p.result == nil || p.client != c || p.envelope != result.VscreenEnvelope || time.Now().After(p.expires) || h.vscreenClientLocked(result.WorkspaceID, result.RuntimeID, c.identity.DaemonID) != c {
		return
	}
	if result.Validate(p.kind) != nil {
		return
	}
	select {
	case p.result <- result:
	default:
	}
}
