package agent

import (
	"context"
	"encoding/json"
	"time"
)

func (c *codexClient) requestHumanApproval(id int, method string, params json.RawMessage) {
	reply := func(approved bool) {
		if method == "item/permissions/requestApproval" {
			if approved {
				c.respond(id, codexPermissionsApprovalResponse(params, nil))
			} else {
				c.respond(id, map[string]any{"permissions": map[string]any{}, "scope": "turn"})
			}
			return
		}
		decision := "decline"
		if approved {
			decision = "accept"
		}
		c.respond(id, map[string]any{"decision": decision})
	}
	if c.cfg.RequestApproval == nil || c.approvalContext == nil || len(params) > 1800 || !json.Valid(params) {
		reply(false)
		return
	}
	c.mu.Lock()
	if c.approvalPending {
		c.mu.Unlock()
		reply(false)
		return
	}
	c.approvalPending = true
	c.mu.Unlock()
	// The reader must keep draining notifications while the reviewer decides.
	go func() {
		defer func() { c.mu.Lock(); c.approvalPending = false; c.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(c.approvalContext, 2*time.Minute)
		defer cancel()
		go func() {
			select {
			case <-c.processDone:
				cancel()
			case <-ctx.Done():
			}
		}()
		approved, err := c.cfg.RequestApproval(ctx, ApprovalRequest{Method: method, Params: append(json.RawMessage(nil), params...)})
		reply(err == nil && ctx.Err() == nil && approved)
	}()
}
