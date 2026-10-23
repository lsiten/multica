package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestEnvironmentLostAllocationRefusalDistinguishesPartialEffects(t *testing.T) {
	for _, mode := range []string{"prepare_rejected", "begin_task_rejected", "partial_allocation"} {
		t.Run(mode, func(t *testing.T) {
			// Given a real physical service and a lost admitted response.
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			owner, err := newEnvironmentPhysicalOwner(t.TempDir(), t.TempDir(), logger)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(owner.close)
			service := &environmentProcessService{physical: owner, daemon: &Daemon{workspaces: map[string]*workspaceState{}}}
			operation := "physical.prepare"
			input := execenv.PhysicalPrepareParams{WorkspaceID: "ws", TaskID: "task", RuntimeID: "unowned-runtime", AgentName: "fixture"}
			var payload any = input
			if mode == "begin_task_rejected" {
				operation = "physical.begin_task"
				payload = struct {
					Physical execenv.PhysicalPrepareParams `json:"physical"`
				}{input}
			}
			entered := make(chan string, 1)
			release := make(chan struct{})
			c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
				config.DetachMutations = true
				config.Capabilities = append(config.Capabilities, operation)
				config.Handler = func(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
					entered <- request.RequestID
					select {
					case <-release:
					case <-ctx.Done():
						return nil, nil
					}
					if mode == "partial_allocation" {
						result, err := owner.prepare(ctx, input)
						if err != nil {
							return environmentOperationResult(nil, err)
						}
						return environmentOperationResult(result, errors.New("fixture partial allocation error"))
					}
					return service.mutate(ctx, request)
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { var result environmentPhysicalResult; done <- c.mutation(ctx, operation, payload, &result) }()
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
			// When the background reader learns the actual completed domain refusal.
			deadline := time.Now().Add(4 * time.Second)
			for {
				c.mu.Lock()
				remaining := len(c.pending)
				consumed := len(c.consumed)
				ack := c.acknowledgement != nil
				c.mu.Unlock()
				if mode != "partial_allocation" && remaining == 0 && consumed == 0 && !ack {
					break
				}
				if mode == "partial_allocation" {
					receipt, err := c.process.Client.QueryOperation(t.Context(), id)
					if err == nil && receipt.State == "completed" {
						if receipt.Error == nil || receipt.Error.Code != "environment_failed" {
							t.Fatalf("partial failure misclassified: %+v", receipt)
						}
						c.reconcilePending(t.Context())
						c.mu.Lock()
						remaining = len(c.pending)
						c.mu.Unlock()
						if remaining != 1 {
							t.Fatal("partial allocation failure was discarded")
						}
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatal("allocation refusal was not reconciled")
				}
				time.Sleep(10 * time.Millisecond)
			}
			// Then proven no-effect refusals retire; possible side effects stay protected.
			owner.mu.Lock()
			allocations := len(owner.preparations)
			owner.mu.Unlock()
			if mode == "partial_allocation" {
				if allocations != 1 {
					t.Fatalf("partial allocation fixture missing: %d", allocations)
				}
			} else {
				if allocations != 0 {
					t.Fatalf("refused allocation created resources: %d", allocations)
				}
				entries, err := os.ReadDir(owner.root)
				if err != nil || len(entries) != 0 {
					t.Fatalf("pure refusal touched workspace storage: %d %v", len(entries), err)
				}
				record, err := runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
				if err != nil {
					t.Fatal(err)
				}
				if _, retained := record.Operations[id]; retained {
					t.Fatal("proven refusal receipt leaked")
				}
			}
		})
	}
}

func TestEnvironmentAllocationMetadataRefusalPrecedesPhysicalEffects(t *testing.T) {
	owner, err := newEnvironmentPhysicalOwner(t.TempDir(), t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.close)
	_, err = owner.prepare(t.Context(), execenv.PhysicalPrepareParams{WorkspaceID: "ws", TaskID: "task"}, &execenv.GCMeta{WorkspaceID: "ws", TaskID: "other-task"})
	var rejected *environmentAllocationRejected
	if !errors.As(err, &rejected) {
		t.Fatalf("metadata refusal not typed: %v", err)
	}
	entries, readErr := os.ReadDir(owner.root)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("metadata refusal created physical resources: %d %v", len(entries), readErr)
	}
}

func TestEnvironmentSynchronousPartialAllocationRetainsReceipt(t *testing.T) {
	owner, err := newEnvironmentPhysicalOwner(t.TempDir(), t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.close)
	input := execenv.PhysicalPrepareParams{WorkspaceID: "ws", TaskID: "task", AgentName: "fixture"}
	c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.Capabilities = append(config.Capabilities, "physical.prepare")
		config.Handler = func(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			result, err := owner.prepare(ctx, input)
			if err != nil {
				return environmentOperationResult(nil, err)
			}
			return environmentOperationResult(result, errors.New("fixture partial allocation error"))
		}
	})
	var output environmentPhysicalResult
	err = c.mutation(t.Context(), "physical.prepare", input, &output)
	var failure *runtimeproc.Error
	if !errors.As(err, &failure) || failure.Code != "environment_failed" {
		t.Fatalf("original domain error lost: %v", err)
	}
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 1 {
		t.Fatalf("partial allocation receipt retired while reservation remains: pending=%d", pending)
	}
	status, err := c.process.Client.Health(t.Context())
	if err != nil || status.Receipts.Completed == 0 {
		t.Fatalf("partial allocation evidence missing: %+v %v", status.Receipts, err)
	}
	reserved, err := execenv.PhysicalRootReserved(output.Reservation.RootDir)
	if err != nil || !reserved {
		t.Fatalf("partial allocation protection lost: reserved=%v %v", reserved, err)
	}
}

func TestEnvironmentEmptyAllocationResultRetainsReceipt(t *testing.T) {
	c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.Capabilities = append(config.Capabilities, "physical.prepare")
		config.Handler = func(context.Context, runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			return nil, nil
		}
	})
	var output environmentPhysicalResult
	err := c.mutation(t.Context(), "physical.prepare", execenv.PhysicalPrepareParams{WorkspaceID: "ws", TaskID: "task"}, &output)
	if err == nil {
		t.Fatal("empty allocation result was accepted")
	}
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 1 {
		t.Fatalf("undecodable allocation result retired: pending=%d", pending)
	}
}
