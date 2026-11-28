package agent

import (
	"context"
	"testing"
	"testing/synctest"
)

func TestCodexWarmTurnApprovalDoesNotClearNextTurnReview(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, input, _ := newTestCodexClient(t)
		oldCtx, cancelOld := context.WithCancel(t.Context())
		defer cancelOld()
		oldDecision := make(chan struct{})
		newDecision := make(chan struct{})
		defer close(newDecision)
		oldCalls, newCalls := 0, 0
		oldCfg := c.cfg
		oldCfg.RequestApproval = func(ctx context.Context, _ ApprovalRequest) (bool, error) {
			oldCalls++
			<-oldDecision
			return false, ctx.Err()
		}
		c.attachTurnHooks(codexTurnHooks{cfg: oldCfg, approvalContext: oldCtx})
		c.handleLine(`{"id":1,"method":"item/commandExecution/requestApproval","params":{"command":"old"}}`)
		synctest.Wait()
		newCfg := c.cfg
		newCfg.RequestApproval = func(ctx context.Context, _ ApprovalRequest) (bool, error) {
			newCalls++
			<-newDecision
			return false, ctx.Err()
		}
		c.detachTurnHooks()
		cancelOld()
		c.attachTurnHooks(codexTurnHooks{cfg: newCfg, approvalContext: t.Context()})
		c.handleLine(`{"id":2,"method":"item/commandExecution/requestApproval","params":{"command":"new"}}`)
		synctest.Wait()
		close(oldDecision)
		synctest.Wait()
		c.handleLine(`{"id":3,"method":"item/commandExecution/requestApproval","params":{"command":"overlap"}}`)
		synctest.Wait()
		if oldCalls != 1 || newCalls != 1 {
			t.Fatalf("reviewer calls old=%d new=%d, want one per turn", oldCalls, newCalls)
		}
		if len(input.Lines()) != 2 {
			t.Fatalf("expected old and overlapping requests declined while new review waits, got %v", input.Lines())
		}
	})
}
