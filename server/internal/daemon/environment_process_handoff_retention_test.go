package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestEnvironmentUsedHandoffRemainsRecoverableUntilDomainSettlement(t *testing.T) {
	// Given an allocation that has become selected before its caller recovers.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	owner, err := newEnvironmentPhysicalOwner(t.TempDir(), t.TempDir(), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.close)
	input := execenv.PhysicalPrepareParams{WorkspaceID: "ws", TaskID: "task", AgentName: "fixture"}
	result, err := owner.prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	owner.preparations[result.Reservation.ID].Selected = true
	entered := make(chan string, 1)
	release := make(chan struct{})
	c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.DetachMutations = true
		config.Capabilities = append(config.Capabilities, "physical.prepare", "physical.recover_handoff")
		config.Handler = func(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			if request.Operation == "physical.recover_handoff" {
				var input environmentHandoffRecovery
				if err := json.Unmarshal(request.Payload, &input); err != nil {
					return environmentOperationResult(nil, err)
				}
				result, err := owner.recoverUnclaimed(ctx, input)
				return environmentOperationResult(result, err)
			}
			entered <- request.RequestID
			select {
			case <-release:
			case <-ctx.Done():
				return nil, nil
			}
			return environmentOperationResult(result, nil)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		var output environmentPhysicalResult
		done <- c.mutation(ctx, "physical.prepare", input, &output)
	}()
	var id string
	select {
	case id = <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("prepare not admitted")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	// When disposition cannot prove that a selected allocation is unused.
	deadline := time.Now().Add(4 * time.Second)
	for {
		c.mu.Lock()
		pending, exists := c.pending[id]
		retained := exists && pending.HandoffRequest == nil && !pending.HandoffRetryAt.IsZero() && len(c.consumed) == 0 && c.acknowledgement == nil
		c.mu.Unlock()
		if retained {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unsettled handoff result was lost or never checked")
		}
		time.Sleep(10 * time.Millisecond)
	}
	receipt, err := c.process.Client.QueryOperation(t.Context(), id)
	if err != nil || receipt.State != "completed" {
		t.Fatalf("original outcome retired before domain settlement: %+v %v", receipt, err)
	}
	if reserved, err := execenv.PhysicalRootReserved(result.Reservation.RootDir); err != nil || !reserved {
		t.Fatalf("used allocation protection removed: %v", err)
	}
	// Then an independently proven owner settlement permits the original ACK.
	claim, err := execenv.ClaimPhysicalRoot(owner.root, result.Reservation)
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.confirmRoot(result.Reservation); err != nil {
		claim.Release()
		t.Fatal(err)
	}
	claim.Release()
	owner.mu.Lock()
	preparation := owner.preparations[result.Reservation.ID]
	preparation.Aborted = true
	preparation.Outcome = &execenv.LocalWorktreeOutcome{}
	err = owner.persist(preparation)
	owner.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(4 * time.Second)
	for {
		c.mu.Lock()
		settled := len(c.pending) == 0 && len(c.consumed) == 0 && c.acknowledgement == nil
		c.mu.Unlock()
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("durable domain settlement did not release the original result")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
