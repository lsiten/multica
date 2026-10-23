package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

type environmentPendingOperation struct {
	Request        runtimeproc.Request
	Scope          environmentMutationScope
	ScopeResolved  bool
	Cause          error
	HandoffRequest *runtimeproc.Request
	HandoffRetryAt time.Time
}

func (pending environmentPendingOperation) uncertainty() *environmentUncertainOperation {
	return &environmentUncertainOperation{RequestID: pending.Request.RequestID, Operation: pending.Request.Operation, Cause: pending.Cause, Scope: pending.Scope}
}

func (c *environmentProcessClient) trackPending(pending environmentPendingOperation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		c.pending = make(map[string]environmentPendingOperation)
	}
	c.pending[pending.Request.RequestID] = pending
	if c.recoveryWake != nil {
		select {
		case c.recoveryWake <- struct{}{}:
		default:
		}
	}
}

func (c *environmentProcessClient) scopeUncertaintyLocked(scope environmentMutationScope) error {
	ids := make([]string, 0, len(c.pending))
	for id := range c.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		pending := c.pending[id]
		if scope.conflicts(pending.Scope) {
			return pending.uncertainty()
		}
	}
	return nil
}

// reconcilePending queries the original operation identity. It does not repeat
// Git/filesystem effects, and absence is conclusive only after admission expires.
func (c *environmentProcessClient) reconcilePending(ctx context.Context) {
	if !c.reconciling.TryLock() {
		return
	}
	defer c.reconciling.Unlock()
	c.mu.Lock()
	pending := make([]environmentPendingOperation, 0, len(c.pending))
	for _, operation := range c.pending {
		pending = append(pending, operation)
	}
	c.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	checking, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, operation := range pending {
		receipt, err := c.process.Client.QueryOperation(checking, operation.Request.RequestID)
		if err != nil {
			var rejection *runtimeproc.Error
			absent := errors.As(err, &rejection) && (rejection.Code == "retired_request" || rejection.Code == "not_found" && time.Now().After(operation.Request.Deadline))
			// Admitted receipts survive selective epoch changes. An absent old
			// epoch identity cannot be admitted later, even before its deadline.
			if absent {
				c.mu.Lock()
				delete(c.pending, operation.Request.RequestID)
				c.mu.Unlock()
			} else if errors.As(err, &rejection) && rejection.Code == "not_found" {
				c.resolvePendingScope(checking, operation)
			}
			continue
		}
		if receipt.State != "completed" {
			c.resolvePendingScope(checking, operation)
			continue
		}
		if allocationRequiresHandoff(operation.Request.Operation) && !c.recoverPreparationHandoff(checking, operation, receipt) {
			c.resolvePendingScope(checking, operation)
			continue
		}
		c.mu.Lock()
		_, tracked := c.pending[operation.Request.RequestID]
		if tracked {
			delete(c.pending, operation.Request.RequestID)
			c.consumeLocked(receipt.RequestID)
			if receipt.Error == nil {
				c.rememberBindingLocked(operation.Request)
			}
		}
		c.mu.Unlock()
		if tracked && c.daemon != nil && c.daemon.logger != nil {
			c.daemon.logger.Info("environment operation reconciled", "operation", operation.Request.Operation, "request_id", operation.Request.RequestID, "error", receipt.Error)
		}
	}
}

// Healthy execution never scans directory scopes. Resolve them only while an
// outcome is genuinely unknown, after a cheap query could not settle it.
func (c *environmentProcessClient) resolvePendingScope(ctx context.Context, operation environmentPendingOperation) {
	if operation.ScopeResolved {
		return
	}
	scope, err := c.readMutationScope(ctx, operation.Request.Operation, operation.Request.Payload)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if current, exists := c.pending[operation.Request.RequestID]; exists {
		current.Scope, current.ScopeResolved = scope, true
		c.pending[operation.Request.RequestID] = current
	}
}

func (c *environmentProcessClient) rememberBinding(request runtimeproc.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rememberBindingLocked(request)
}

func (c *environmentProcessClient) rememberBindingLocked(request runtimeproc.Request) {
	if request.Operation == "environment.bind" {
		digest := sha256.Sum256(request.Payload)
		c.bindingDigest = hex.EncodeToString(digest[:])
	}
}

func (c *environmentProcessClient) capacityError(status runtimeproc.Status, cause error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	message := fmt.Sprintf("environment receipt capacity unavailable: total=%d pending=%d completed=%d limit=%d", status.Receipts.Total, status.Receipts.Pending, status.Receipts.Completed, status.Receipts.Limit)
	ids := make([]string, 0, len(c.pending))
	for id := range c.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > 0 {
		pending := c.pending[ids[0]]
		message += fmt.Sprintf("; unresolved operation=%s request=%s", pending.Request.Operation, ids[0])
		if len(pending.Scope.Paths) > 0 {
			message += "; protected directory=" + pending.Scope.Paths[0]
		}
	}
	return fmt.Errorf("%s; wait for reconciliation before retrying: %w", message, cause)
}
