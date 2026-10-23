package daemon

import (
	"context"
	"time"
)

func (c *environmentProcessClient) startRecovery() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.recoveryCancel != nil || c.closed {
		return
	}
	life, cancel := context.WithCancel(context.Background())
	c.recoveryCancel = cancel
	c.recoveryDone = make(chan struct{})
	c.recoveryWake = make(chan struct{}, 1)
	go c.recoveryLoop(life)
}

func (c *environmentProcessClient) stopRecovery() {
	c.mu.Lock()
	c.closed = true
	cancel, done := c.recoveryCancel, c.recoveryDone
	c.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (c *environmentProcessClient) recoveryLoop(ctx context.Context) {
	defer close(c.recoveryDone)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-c.recoveryWake:
		}
		c.mu.Lock()
		work := len(c.pending) > 0 || len(c.consumed) > 0 || c.acknowledgement != nil
		c.mu.Unlock()
		if !work {
			continue
		}
		// Never wait behind an ordinary call: it may itself need a physical
		// confirmation, which must retain its independent admission channel.
		select {
		case c.gate <- struct{}{}:
		default:
			continue
		}
		c.reconcilePending(ctx)
		c.mu.Lock()
		if c.active == 0 && (len(c.consumed) > 0 || c.acknowledgement != nil) {
			cleanup, cancel := context.WithTimeout(ctx, 2*time.Second)
			c.retirementErr = c.retireReceipts(cleanup)
			cancel()
		}
		c.mu.Unlock()
		<-c.gate
	}
}
