package daemonws

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// SubmitVscreenCommand records acceptance before sending; a timeout never resends work.
func (h *Hub) SubmitVscreenCommand(workspaceID, runtimeID, daemonID, userID, commandID string, kind protocol.VscreenCommandKind) (protocol.VscreenCommandReceipt, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneVscreenLocked(time.Now())
	for _, p := range h.vscreenPending {
		if p.envelope.WorkspaceID == workspaceID && p.envelope.RuntimeID == runtimeID && p.receipt.CommandID == commandID {
			return protocol.VscreenCommandReceipt{}, ErrVscreenReplay
		}
	}
	c := h.vscreenClientLocked(workspaceID, runtimeID, daemonID)
	if c == nil {
		return protocol.VscreenCommandReceipt{}, ErrVscreenUnavailable
	}
	if len(h.vscreenPending) >= 1024 {
		return protocol.VscreenCommandReceipt{}, ErrVscreenCapacity
	}
	envelope := protocol.VscreenEnvelope{WorkspaceID: workspaceID, RuntimeID: runtimeID, DaemonGeneration: c.vscreenGeneration, RequestID: uuid.NewString()}
	command := protocol.VscreenCommand{VscreenEnvelope: envelope, CommandID: commandID, Kind: kind}
	if err := command.Validate(); err != nil {
		return protocol.VscreenCommandReceipt{}, err
	}
	receipt := protocol.VscreenCommandReceipt{VscreenEnvelope: envelope, CommandID: commandID, ReceiptID: uuid.NewString(), State: protocol.VscreenReceiptPending}
	if h.vscreenPending == nil {
		h.vscreenPending = make(map[string]*vscreenPending)
	}
	h.vscreenPending[envelope.RequestID] = &vscreenPending{client: c, envelope: envelope, receipt: receipt, kind: string(kind), userID: userID, expires: time.Now().Add(30 * time.Minute)}
	if !c.trySend(mustMarshalRaw(protocol.Message{Type: protocol.EventVscreenCommand, Payload: mustMarshalRaw(command)})) {
		delete(h.vscreenPending, envelope.RequestID)
		return protocol.VscreenCommandReceipt{}, ErrVscreenUnavailable
	}
	return receipt, nil
}

// VscreenCommandStatus is scoped to the creator or current runtime owner.
func (h *Hub) VscreenCommandStatus(workspaceID, runtimeID, daemonID, userID, commandID string, owner bool) (protocol.VscreenCommandReceipt, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneVscreenLocked(time.Now())
	for _, p := range h.vscreenPending {
		if p.receipt.CommandID != commandID || p.envelope.WorkspaceID != workspaceID || p.envelope.RuntimeID != runtimeID || (!owner && p.userID != userID) {
			continue
		}
		if h.vscreenClientLocked(workspaceID, runtimeID, daemonID) != p.client {
			return protocol.VscreenCommandReceipt{}, ErrVscreenStale
		}
		receipt := p.receipt
		if (receipt.State == protocol.VscreenReceiptPending || receipt.State == protocol.VscreenReceiptRunning) && time.Until(p.expires) < 29*time.Minute {
			receipt.State = protocol.VscreenReceiptUnknown
			receipt.Reason = protocol.VscreenActionUncertainReason
		}
		return receipt, nil
	}
	return protocol.VscreenCommandReceipt{}, ErrVscreenNotFound
}

func (c *client) handleVscreenReceipt(raw json.RawMessage) {
	if len(raw) > 16*1024 {
		return
	}
	var receipt protocol.VscreenCommandReceipt
	if json.Unmarshal(raw, &receipt) != nil || receipt.Validate() != nil {
		return
	}
	h := c.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.vscreenPending[receipt.RequestID]
	if p == nil || p.result != nil || p.client != c || p.envelope != receipt.VscreenEnvelope || p.receipt.CommandID != receipt.CommandID || time.Now().After(p.expires) || h.vscreenClientLocked(receipt.WorkspaceID, receipt.RuntimeID, c.identity.DaemonID) != c {
		return
	}
	switch p.receipt.State {
	case protocol.VscreenReceiptSucceeded:
		if p.kind == string(protocol.VscreenCommandDisable) && p.receipt.ReceiptID == receipt.ReceiptID && receipt.State == protocol.VscreenReceiptSucceeded {
			c.trySend(mustMarshalRaw(protocol.Message{Type: protocol.EventVscreenResult, Payload: mustMarshalRaw(p.receipt)}))
		}
		return
	case protocol.VscreenReceiptFailed, protocol.VscreenReceiptUnknown:
		return
	}
	if p.kind == string(protocol.VscreenCommandDisable) && receipt.State == protocol.VscreenReceiptSucceeded {
		if p.cleanupStarted {
			return
		}
		p.cleanupStarted = true
		p.receipt = receipt
		p.receipt.State = protocol.VscreenReceiptRunning
		go c.finishVscreenDisable(receipt)
		return
	}
	if p.receipt.State == protocol.VscreenReceiptRunning && receipt.State == protocol.VscreenReceiptPending {
		return
	}
	p.receipt = receipt
}
