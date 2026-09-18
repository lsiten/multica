package daemonws

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// VscreenConnection binds a validated envelope to an exact authenticated socket.
// Its private client cannot be constructed from a daemon-supplied identity.
type VscreenConnection struct {
	client   *client
	envelope protocol.VscreenEnvelope
}

// Identity returns the connection's authenticated identity.
func (c VscreenConnection) Identity() ClientIdentity { return c.client.identity }

// WithCurrent fences persistence against socket replacement and removal. Native
// queries must finish before entering this section so the read pump remains free.
func (c VscreenConnection) WithCurrent(ctx context.Context, persist func() error) error {
	h := c.client.hub
	h.mu.RLock()
	defer h.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.client.ctx.Err() != nil || !h.clients[c.client] || h.vscreenClientLocked(c.envelope.WorkspaceID, c.envelope.RuntimeID, c.client.identity.DaemonID) != c.client {
		return ErrVscreenStale
	}
	return persist()
}

// VscreenConnection authenticates the report scope and requires the newest socket.
func (h *Hub) VscreenConnection(envelope protocol.VscreenEnvelope, daemonID string) (VscreenConnection, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.vscreenClientLocked(envelope.WorkspaceID, envelope.RuntimeID, daemonID)
	if c == nil {
		return VscreenConnection{}, ErrVscreenUnavailable
	}
	if c.vscreenGeneration != envelope.DaemonGeneration {
		return VscreenConnection{}, ErrVscreenStale
	}
	return VscreenConnection{client: c, envelope: envelope}, nil
}

// VscreenInterventionHandler validates native state and commits the report before returning.
type VscreenInterventionHandler func(context.Context, VscreenConnection, protocol.VscreenIntervention) (int64, string)

// SetVscreenInterventionHandler installs the production persistence callback.
func (h *Hub) SetVscreenInterventionHandler(fn VscreenInterventionHandler) {
	h.interventionMu.Lock()
	h.onIntervention = fn
	h.interventionMu.Unlock()
}

func (c *client) handleVscreenIntervention(raw json.RawMessage) {
	if len(raw) > 8192 {
		return
	}
	var report protocol.VscreenIntervention
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&report) != nil || decoder.Decode(new(json.RawMessage)) != io.EOF || report.Validate() != nil {
		// Only echo bounded, valid correlation fields from malformed reports.
		if report.VscreenEnvelope.Validate() == nil {
			c.sendInterventionAck(report, 0, "invalid_report")
		}
		return
	}
	if c.identity.DaemonID == "" || len(c.identity.AuthorizedWorkspaceIDs()) == 0 || !c.identity.AllowsWorkspace(report.WorkspaceID) || !c.allowsRuntime(report.RuntimeID) {
		c.sendInterventionAck(report, 0, "permission_denied")
		return
	}
	scope, err := c.hub.VscreenConnection(report.VscreenEnvelope, c.identity.DaemonID)
	if err != nil || scope.client != c {
		c.sendInterventionAck(report, 0, "stale_generation")
		return
	}
	c.hub.interventionMu.RLock()
	handler := c.hub.onIntervention
	c.hub.interventionMu.RUnlock()
	if handler == nil {
		c.sendInterventionAck(report, 0, "daemon_unavailable")
		return
	}
	select {
	case c.rpcSem <- struct{}{}:
	default:
		c.sendInterventionAck(report, 0, "request_capacity")
		return
	}
	go func() {
		defer func() { <-c.rpcSem }()
		// A report owns one bounded RPC slot and the socket lifetime. The callback
		// queries native state on this same read pump, so it must run asynchronously.
		ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
		defer cancel()
		version, reason := handler(ctx, scope, report)
		if ctx.Err() != nil {
			version, reason = 0, "daemon_timeout"
		}
		c.sendInterventionAck(report, version, reason)
	}()
}

func (c *client) sendInterventionAck(report protocol.VscreenIntervention, version int64, reason string) {
	c.hub.mu.RLock()
	defer c.hub.mu.RUnlock()
	if reason == "" && (c.ctx.Err() != nil || c.hub.vscreenClientLocked(report.WorkspaceID, report.RuntimeID, c.identity.DaemonID) != c || report.DaemonGeneration != c.vscreenGeneration) {
		version, reason = 0, "stale_generation"
	}
	ack := protocol.VscreenInterventionAck{VscreenEnvelope: report.VscreenEnvelope, InterventionID: report.InterventionID, Accepted: reason == "", Version: version, Reason: reason}
	if ack.Validate() != nil {
		ack.Accepted, ack.Version, ack.Reason = false, 0, "intervention_failed"
		if ack.Validate() != nil {
			return
		}
	}
	c.trySend(mustMarshalRaw(protocol.Message{Type: protocol.EventVscreenInterventionAck, Payload: mustMarshalRaw(ack)}))
}
