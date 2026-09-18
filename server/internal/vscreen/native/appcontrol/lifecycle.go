package appcontrol

import (
	"context"
	"errors"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Close restores only still-owned automatically moved windows. Failed cleanup retains
// claims and can be retried; apps and unsaved documents are never terminated.
func (c *Controller) Close(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true
	if c.activeCancel != nil {
		c.activeCancel()
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, operationLimit)
	defer cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.gate:
	}
	defer func() { c.gate <- struct{}{} }()
	if c.backend == nil {
		return nil
	}
	if err := c.backend.call(ctx, "quiesce", nil, nil); err != nil {
		return err
	}
	if err := c.disposeWindows(ctx, nil); err != nil {
		return err
	}
	if err := c.backend.close(); err != nil {
		return err
	}
	c.backend = nil
	return nil
}

func (c *Controller) disposeWindows(ctx context.Context, key *protocol.ResourceKey) error {
	var failures []error
	for handle, w := range c.windows {
		if key != nil && w.display.Resource != *key {
			continue
		}
		if !w.nativeReleased && !w.human {
			if err := c.backend.call(ctx, "restore", map[string]any{"Window": w.window}, nil); err != nil {
				failures = append(failures, err)
				continue
			}
		}
		if !w.nativeReleased {
			if err := c.backend.call(ctx, "forget", map[string]any{"Window": w.window}, nil); err != nil {
				failures = append(failures, err)
				continue
			}
			w.nativeReleased = true
		}
		if err := w.claim.ReleaseAfterQuiescence(); err != nil {
			failures = append(failures, err)
			continue
		}
		delete(c.windows, handle)
	}
	return errors.Join(failures...)
}

// Dispose revokes one runtime, restores its owned windows and releases only its claims.
func (c *Controller) Dispose(ctx context.Context, key protocol.ResourceKey) error {
	if key.Validate() != nil {
		return refusal("stale_authority")
	}
	c.freeze(key)
	ctx, leave, err := c.enter(ctx, key)
	if err != nil {
		return err
	}
	defer leave()
	if err = c.backend.call(ctx, "quiesce", map[string]any{"Resource": key}, nil); err != nil {
		return err
	}
	if err = c.disposeWindows(ctx, &key); err != nil {
		return err
	}
	for id := range c.actions {
		if id.Grant.Resource == key {
			delete(c.actions, id)
		}
	}
	for grant := range c.sequence {
		if grant.Resource == key {
			delete(c.sequence, grant)
		}
	}
	return nil
}
