package agent

import (
	"context"
	"encoding/json"
	"slices"
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
		if method == "execCommandApproval" || method == "applyPatchApproval" {
			decision = "denied"
			if approved {
				decision = "approved"
			}
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
	var requestItem struct {
		ItemID       string         `json:"itemId"`
		CallID       string         `json:"call_id"`
		LegacyCallID string         `json:"callId"`
		FileChanges  map[string]any `json:"fileChanges"`
	}
	_ = json.Unmarshal(params, &requestItem)
	itemID := requestItem.ItemID
	if itemID == "" {
		itemID = requestItem.CallID
	}
	if itemID == "" {
		itemID = requestItem.LegacyCallID
	}
	files := slices.Clone(c.approvalFiles[itemID])
	c.mu.Unlock()
	if len(files) == 0 && len(requestItem.FileChanges) > 0 {
		files = approvalFilesFromChanges(codexNormalizeLegacyChanges(requestItem.FileChanges))
	}
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
		approved, err := c.cfg.RequestApproval(ctx, ApprovalRequest{Method: method, Params: append(json.RawMessage(nil), params...), FileChanges: files})
		reply(err == nil && ctx.Err() == nil && approved)
	}()
}

func (c *codexClient) recordApprovalFiles(itemID string, changes []any) {
	if itemID == "" {
		return
	}
	files := approvalFilesFromChanges(changes)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.approvalFiles == nil {
		c.approvalFiles = make(map[string][]ApprovalFileChange)
	}
	c.approvalFiles[itemID] = files
}

func approvalFilesFromChanges(changes []any) []ApprovalFileChange {
	files := make([]ApprovalFileChange, 0, len(changes))
	for _, change := range changes {
		entry, ok := change.(map[string]any)
		if !ok {
			continue
		}
		path, _ := entry["path"].(string)
		kind, _ := entry["kind"].(string)
		move, _ := entry["move_path"].(string)
		if path != "" {
			files = append(files, ApprovalFileChange{Path: path, Kind: kind, MovePath: move})
		}
	}
	return files
}

func (c *codexClient) forgetApprovalFiles(itemID string) {
	c.mu.Lock()
	delete(c.approvalFiles, itemID)
	c.mu.Unlock()
}
