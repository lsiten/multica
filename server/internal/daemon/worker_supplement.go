package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// wakeSupplement is a content-free hint, never an instruction or execution grant.
func (s *workerProcessService) wakeSupplement(raw json.RawMessage) (json.RawMessage, *runtimeproc.Error) {
	var identity protocol.ExecutionIdentity
	if json.Unmarshal(raw, &identity) != nil {
		return nil, &runtimeproc.Error{Code: "malformed", Message: "invalid supplement execution identity"}
	}
	s.mu.Lock()
	bound := s.bound && s.authorization.Launchable && identity == s.identity
	s.mu.Unlock()
	if !bound {
		return nil, &runtimeproc.Error{Code: "execution_changed", Message: "supplement does not belong to this worker"}
	}
	s.supplements.notify(identity.TaskID)
	return json.RawMessage(`{"notified":true}`), nil
}

func (c *workerProcessClient) forwardSupplementWakeups(ctx context.Context, wakeup <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-wakeup:
			started := time.Now()
			callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := c.notifySupplement(callCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				slog.Debug("worker supplement wakeup unavailable", "task_id", c.executionIdentity.TaskID, "error", err)
			} else if err == nil {
				slog.Debug("worker supplement wakeup delivered", "task_id", c.executionIdentity.TaskID, "elapsed_ms", time.Since(started).Milliseconds())
			}
		}
	}
}

// Lost hint receipts must not poison bind/run authority. Durable input remains
// claimable by the worker's normal polling path, even if this IPC call fails.
func (c *workerProcessClient) notifySupplement(ctx context.Context) error {
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.gate }()
	status, err := c.transport.Health(ctx)
	if err != nil {
		return err
	}
	request, err := c.transport.Request("worker.supplement-wakeup", status.Fence, marshalRaw(c.executionIdentity))
	if err != nil {
		return err
	}
	response, err := c.transport.Call(ctx, request)
	if err != nil {
		return err
	}
	if response.Receipt == nil || response.Receipt.State != "completed" {
		return errors.New("worker supplement wakeup receipt pending")
	}
	var resultErr error
	if response.Receipt.Error != nil {
		resultErr = response.Receipt.Error
	}
	ack, err := c.transport.Request("acknowledge", response.Status.Fence, marshalRaw(runtimeproc.AcknowledgePayload{RequestIDs: []string{request.RequestID}}))
	if err != nil {
		return err
	}
	response, err = c.transport.Call(ctx, ack)
	if err != nil {
		return errors.Join(resultErr, err)
	}
	if response.Receipt == nil || response.Receipt.State != "completed" {
		return errors.New("worker supplement wakeup acknowledgement pending")
	}
	if response.Receipt.Error != nil {
		return response.Receipt.Error
	}
	return resultErr
}
