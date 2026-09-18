package daemonws

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// VscreenDisabledHandler retires persisted server state after a native disable.
// Its acknowledgement grants no input or physical handoff authority.
type VscreenDisabledHandler func(context.Context, VscreenConnection, protocol.VscreenCommandReceipt) error

func (h *Hub) SetVscreenDisabledHandler(fn VscreenDisabledHandler) {
	h.interventionMu.Lock()
	defer h.interventionMu.Unlock()
	h.onVscreenDisabled = fn
}

func (c *client) finishVscreenDisable(receipt protocol.VscreenCommandReceipt) {
	h := c.hub
	h.interventionMu.RLock()
	handler := h.onVscreenDisabled
	h.interventionMu.RUnlock()
	select {
	case c.rpcSem <- struct{}{}:
	default:
		c.storeVscreenDisable(receipt, ErrVscreenCapacity)
		return
	}
	go func() {
		defer func() { <-c.rpcSem }()
		ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
		defer cancel()
		err := ErrVscreenUnavailable
		if handler != nil {
			err = handler(ctx, VscreenConnection{client: c, envelope: receipt.VscreenEnvelope}, receipt)
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		c.storeVscreenDisable(receipt, err)
	}()
}
func (c *client) storeVscreenDisable(receipt protocol.VscreenCommandReceipt, err error) {
	h := c.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.vscreenPending[receipt.RequestID]
	if p == nil || p.client != c || p.envelope != receipt.VscreenEnvelope || p.receipt.CommandID != receipt.CommandID || h.vscreenClientLocked(receipt.WorkspaceID, receipt.RuntimeID, c.identity.DaemonID) != c {
		return
	}
	if err != nil {
		receipt.State = protocol.VscreenReceiptUnknown
		receipt.Reason = protocol.VscreenActionUncertainReason
	}
	p.receipt = receipt
	if err == nil {
		c.trySend(mustMarshalRaw(protocol.Message{Type: protocol.EventVscreenResult, Payload: mustMarshalRaw(receipt)}))
	}
}

// WithVscreenLifecycle keeps native verification and persistence ordered across
// reports and cleanup on this socket. A delayed report cannot recreate a pending
// row after disable committed. The read pump never takes this gate.
func (c VscreenConnection) WithVscreenLifecycle(ctx context.Context, fn func() error) error {
	h := c.client.hub
	h.mu.Lock()
	if c.client.vscreenLifecycle == nil {
		c.client.vscreenLifecycle = make(chan struct{}, 1)
	}
	gate := c.client.vscreenLifecycle
	h.mu.Unlock()
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}
