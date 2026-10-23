package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func allocationRequiresHandoff(operation string) bool {
	return operation == "physical.prepare" || operation == "physical.begin_task"
}

// recoverPreparationHandoff retains the original result until a separately
// journaled disposition proves its allocation settled. A lost disposition
// response is queried under its original identity, never blindly reissued.
func (c *environmentProcessClient) recoverPreparationHandoff(ctx context.Context, pending environmentPendingOperation, receipt runtimeproc.Receipt) bool {
	if receipt.Error != nil {
		return receipt.Error.Code == "allocation_rejected"
	}
	if time.Now().Before(pending.HandoffRetryAt) {
		return false
	}
	var result environmentPhysicalResult
	if json.Unmarshal(receipt.Result, &result) != nil || result.Reservation.ID == "" {
		return false
	}
	var input execenv.PhysicalPrepareParams
	if pending.Request.Operation == "physical.begin_task" {
		var begin struct {
			Physical execenv.PhysicalPrepareParams `json:"physical"`
		}
		if json.Unmarshal(pending.Request.Payload, &begin) != nil {
			return false
		}
		input = begin.Physical
	} else if json.Unmarshal(pending.Request.Payload, &input) != nil {
		return false
	}
	if input.TaskID != result.Reservation.TaskID || input.WorkspaceID != result.Reservation.WorkspaceID {
		return false
	}
	request := pending.HandoffRequest
	var outcome runtimeproc.Receipt
	if request != nil {
		queried, err := c.process.Client.QueryOperation(ctx, request.RequestID)
		if err != nil {
			var rejection *runtimeproc.Error
			if errors.As(err, &rejection) && (rejection.Code == "retired_request" || rejection.Code == "not_found" && time.Now().After(request.Deadline)) {
				c.updateHandoffRecovery(pending.Request.RequestID, nil)
			}
			return false
		}
		outcome = queried
	} else {
		payload, err := json.Marshal(environmentHandoffRecovery{Reservation: result.Reservation})
		if err != nil {
			return false
		}
		status, err := c.process.Client.Health(ctx)
		if err != nil {
			return false
		}
		created, err := c.process.Client.Request("physical.recover_handoff", status.Fence, payload)
		if err != nil {
			return false
		}
		request = &created
		c.updateHandoffRecovery(pending.Request.RequestID, request)
		response, err := c.process.Client.Call(ctx, created)
		if err != nil {
			if runtimeproc.RejectedBeforeAdmission(err) {
				c.updateHandoffRecovery(pending.Request.RequestID, nil)
			}
			return false
		}
		if response.Receipt == nil {
			return false
		}
		outcome = *response.Receipt
	}
	if outcome.State != "completed" {
		return false
	}
	c.mu.Lock()
	c.consumeLocked(outcome.RequestID)
	c.mu.Unlock()
	c.updateHandoffRecovery(pending.Request.RequestID, nil)
	var settled environmentHandoffResult
	return outcome.Error == nil && json.Unmarshal(outcome.Result, &settled) == nil && settled.Settled
}

func (c *environmentProcessClient) updateHandoffRecovery(id string, request *runtimeproc.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if current, exists := c.pending[id]; exists {
		current.HandoffRequest = request
		current.HandoffRetryAt = time.Now().Add(time.Second)
		current.Cause = errors.New("physical allocation awaits confirmed handoff settlement")
		c.pending[id] = current
	}
}
