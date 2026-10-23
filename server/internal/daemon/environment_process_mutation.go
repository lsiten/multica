package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func (c *environmentProcessClient) mutation(ctx context.Context, operation string, input, output any) error {
	encoded, err := json.Marshal(input)
	if err != nil {
		return err
	}
	gate := c.gate
	confirmation := operation == "physical_finish_confirm"
	if confirmation {
		gate = c.confirmations
	}
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	defer func() { <-gate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	c.reconcilePending(ctx)
	c.mu.Lock()
	isolationNeeded := len(c.pending) > 0
	c.mu.Unlock()
	scope := environmentMutationScope{Global: true}
	if isolationNeeded {
		scope, err = c.readMutationScope(ctx, operation, encoded)
		if err != nil {
			return err
		}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("environment client is closed")
	}
	if !confirmation {
		if problem := c.scopeUncertaintyLocked(scope); problem != nil {
			c.mu.Unlock()
			return problem
		}
	}
	if c.active == 0 && (len(c.consumed) > 0 || c.acknowledgement != nil) {
		cleanup, cancel := context.WithTimeout(ctx, 2*time.Second)
		c.retirementErr = c.retireReceipts(cleanup)
		cancel()
	}
	c.active++
	c.mu.Unlock()
	var receipt *runtimeproc.Receipt
	defer func() { c.finishMutation(ctx, receipt) }()
	pending := environmentPendingOperation{Request: runtimeproc.Request{Operation: operation, Payload: encoded}, Scope: scope, ScopeResolved: isolationNeeded}
	response, err := c.dispatchMutation(ctx, &pending)
	if err != nil {
		return err
	}
	receipt = response.Receipt
	if receipt == nil || receipt.State != "completed" {
		return errors.New("environment operation returned no completed receipt")
	}
	if output != nil && (len(receipt.Result) > 0 || allocationRequiresHandoff(operation) && receipt.Error == nil) {
		if err := json.Unmarshal(receipt.Result, output); err != nil {
			if allocationRequiresHandoff(operation) {
				pending.Cause = err
				c.trackPending(pending)
				receipt = nil
			}
			return err
		}
	}
	if receipt.Error != nil {
		failure := receipt.Error
		if allocationRequiresHandoff(operation) && failure.Code != "allocation_rejected" {
			pending.Cause = failure
			c.trackPending(pending)
			receipt = nil
		}
		return failure
	}
	return nil
}

func (c *environmentProcessClient) readMutationScope(ctx context.Context, operation string, payload json.RawMessage) (environmentMutationScope, error) {
	encoded, err := json.Marshal(struct {
		Operation string          `json:"operation"`
		Payload   json.RawMessage `json:"payload"`
	}{operation, payload})
	if err != nil {
		return environmentMutationScope{}, err
	}
	raw, err := c.process.Client.Read(ctx, "environment.mutation_scope", encoded)
	if err != nil {
		return environmentMutationScope{}, err
	}
	var scope environmentMutationScope
	if err := decodeEnvironmentPayload(raw, &scope); err != nil {
		return environmentMutationScope{}, err
	}
	return scope, nil
}

// Only explicit pre-admission refusals may generate another request. An admitted
// operation is queried under its original identity and is never replayed here.
func (c *environmentProcessClient) dispatchMutation(ctx context.Context, pending *environmentPendingOperation) (runtimeproc.Response, error) {
	waiting, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		status, err := c.process.Client.Health(ctx)
		if err != nil {
			return runtimeproc.Response{}, err
		}
		request, err := c.process.Client.Request(pending.Request.Operation, status.Fence, pending.Request.Payload)
		if err != nil {
			return runtimeproc.Response{}, err
		}
		// A caller timeout stops waiting, while an accepted mutation retains its
		// own bounded execution window so that it can publish a durable outcome.
		request.Deadline = time.Now().Add(3 * time.Minute)
		if err := ctx.Err(); err != nil {
			return runtimeproc.Response{}, err
		}
		pending.Request = request
		response, callErr := c.process.Client.Call(ctx, request)
		if callErr == nil && response.Receipt != nil && response.Receipt.State == "completed" {
			if response.Receipt.Error == nil {
				c.rememberBinding(request)
			}
			return response, nil
		}
		var rejection *runtimeproc.Error
		if errors.As(callErr, &rejection) && slices.Contains([]string{"resource_exhausted", "stale_fence", "retired_request"}, rejection.Code) {
			if err := c.waitForAdmission(waiting, response.Status, callErr); err != nil {
				if ctx.Err() != nil {
					return response, context.Cause(ctx)
				}
				return response, err
			}
			continue
		}
		if runtimeproc.RejectedBeforeAdmission(callErr) {
			return response, callErr
		}
		if callErr == nil {
			callErr = errors.New("pending operation receipt")
		}
		pending.Cause = callErr
		c.trackPending(*pending)
		if errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded) {
			return response, callErr
		}
		return response, pending.uncertainty()
	}
}

func (c *environmentProcessClient) waitForAdmission(ctx context.Context, status runtimeproc.Status, cause error) error {
	c.reconcilePending(ctx)
	c.mu.Lock()
	// This caller received an explicit refusal and has no active server write.
	// Other confirmation callers must finish consuming their results first.
	if c.active == 1 && (len(c.consumed) > 0 || c.acknowledgement != nil) {
		c.retirementErr = c.retireReceipts(ctx)
	}
	c.mu.Unlock()
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return c.capacityError(status, cause)
	case <-timer.C:
		return nil
	}
}
