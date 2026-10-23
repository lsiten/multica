package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// finishMutation retires only results consumed by callers, including domain and
// decoding failures. Pending and unconsumed outcomes survive each replay epoch.
func (c *environmentProcessClient) finishMutation(ctx context.Context, receipt *runtimeproc.Receipt) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active--
	if receipt != nil && receipt.State == "completed" {
		c.consumeLocked(receipt.RequestID)
	}
	if c.active != 0 || len(c.consumed) == 0 && c.acknowledgement == nil {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	// A failed housekeeping response never replaces the consumed domain result.
	c.retirementErr = c.retireReceipts(cleanup)
}

func (c *environmentProcessClient) consumeLocked(id string) {
	if c.consumed == nil {
		c.consumed = make(map[string]struct{})
	}
	c.consumed[id] = struct{}{}
}

// retireReceipts requires client admission exclusion. A missing ACK can only be
// replaced after its original deadline, when delayed admission is impossible.
func (c *environmentProcessClient) retireReceipts(ctx context.Context) error {
	if c.acknowledgement != nil {
		receipt, err := c.process.Client.QueryOperation(ctx, c.acknowledgement.RequestID)
		if err == nil {
			if receipt.State != "completed" || receipt.Error != nil {
				return &environmentUncertainOperation{RequestID: receipt.RequestID, Operation: "acknowledge", Cause: errors.New("receipt retirement is not complete")}
			}
			c.completeAcknowledgementLocked()
		} else {
			var rejection *runtimeproc.Error
			if !errors.As(err, &rejection) || rejection.Code != "not_found" || time.Now().Before(c.acknowledgement.Deadline) {
				return &environmentUncertainOperation{RequestID: c.acknowledgement.RequestID, Operation: "acknowledge", Cause: err}
			}
			c.acknowledgement = nil
		}
	}
	if len(c.consumed) == 0 {
		return nil
	}
	status, err := c.process.Client.Health(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(c.consumed))
	for id := range c.consumed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	payload, err := json.Marshal(runtimeproc.AcknowledgePayload{RequestIDs: ids})
	if err != nil {
		return err
	}
	for attempt := range 3 {
		request, err := c.process.Client.Request("acknowledge", status.Fence, payload)
		if err != nil {
			return err
		}
		c.acknowledgement = &request
		response, err := c.process.Client.Call(ctx, request)
		if err != nil {
			if runtimeproc.RejectedBeforeAdmission(err) {
				c.acknowledgement = nil
				var rejection *runtimeproc.Error
				if attempt < 2 && errors.As(err, &rejection) && (rejection.Code == "stale_fence" || rejection.Code == "retired_request") {
					status, err = c.process.Client.Health(ctx)
					if err != nil {
						return err
					}
					continue
				}
				return err
			}
			return &environmentUncertainOperation{RequestID: request.RequestID, Operation: "acknowledge", Cause: err}
		}
		if response.Receipt == nil || response.Receipt.State != "completed" || response.Receipt.Error != nil {
			return &environmentUncertainOperation{RequestID: request.RequestID, Operation: "acknowledge", Cause: errors.New("receipt retirement is not complete")}
		}
		c.completeAcknowledgementLocked()
		return nil
	}
	return nil
}

func (c *environmentProcessClient) completeAcknowledgementLocked() {
	var payload runtimeproc.AcknowledgePayload
	if err := json.Unmarshal(c.acknowledgement.Payload, &payload); err == nil {
		for _, id := range payload.RequestIDs {
			delete(c.consumed, id)
		}
	}
	c.acknowledgement = nil
}
