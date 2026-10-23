package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestEnvironmentCanceledDetachedMutationRecoversWithoutReplay(t *testing.T) {
	for _, domainFailure := range []bool{false, true} {
		name := "success"
		if domainFailure {
			name = "domain_error"
		}
		t.Run(name, func(t *testing.T) {
			admitted := make(chan string, 1)
			release := make(chan struct{})
			var calls atomic.Int32
			c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
				config.DetachMutations = true
				config.Handler = func(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
					calls.Add(1)
					admitted <- request.RequestID
					select {
					case <-release:
					case <-ctx.Done():
						return nil, nil
					}
					if domainFailure {
						return nil, &runtimeproc.Error{Code: "domain_failure", Message: "late terminal error"}
					}
					return json.RawMessage(`{}`), nil
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- c.mutation(ctx, "review.run", struct{}{}, nil) }()
			var id string
			select {
			case id = <-admitted:
			case <-time.After(3 * time.Second):
				t.Fatal("mutation not admitted")
			}
			cancel()
			callErr := <-done
			if !errors.Is(callErr, context.Canceled) {
				t.Fatalf("expected cancellation: %v", callErr)
			}
			receipt, err := c.process.Client.QueryOperation(t.Context(), id)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if receipt.State != "pending" {
				t.Fatalf("unexpected value, want pending: %v", receipt.State)
			}
			c.mu.Lock()
			_, retained := c.pending[id]
			c.mu.Unlock()
			if !(retained) {
				t.Fatalf("expected true: %v", retained)
			}
			close(release)
			recoveryDeadline := time.NewTimer(4 * time.Second)
			defer recoveryDeadline.Stop()
			poll := time.NewTicker(10 * time.Millisecond)
			defer poll.Stop()
			for {
				c.mu.Lock()
				recovered := len(c.pending) == 0 && len(c.consumed) == 0 && c.acknowledgement == nil
				c.mu.Unlock()
				if recovered {
					break
				}
				select {
				case <-poll.C:
				case <-recoveryDeadline.C:
					t.Fatal("original request did not recover and retire")
				}
			}
			if calls.Load() != int32(1) {
				t.Fatalf("unexpected value, want int32(1): %v", calls.Load())
			}
			record, err := runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			_, retained = record.Operations[id]
			if retained {
				t.Fatalf("expected false: %v", retained)
			}
			if len(record.Operations) > 1 {
				t.Fatalf("exceeded bound: %v", len(record.Operations))
			}
		})
	}
}

func TestEnvironmentPendingResourceAllowsIndependentMutations(t *testing.T) {
	var writes atomic.Int32
	c, _, admitted := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		original := config.Handler
		config.Handler = func(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			writes.Add(1)
			return original(ctx, request)
		}
		config.ReadHandler = func(_ context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			var input struct {
				Payload environmentMutationScope `json:"payload"`
			}
			if err := json.Unmarshal(request.Payload, &input); err != nil {
				return nil, &runtimeproc.Error{Code: "invalid", Message: err.Error()}
			}
			return environmentOperationResult(input.Payload, nil)
		}
	})
	a := environmentMutationScope{WorkspaceID: "ws", Keys: []string{"family-a"}, Paths: []string{"/workspace/a"}}
	b := environmentMutationScope{WorkspaceID: "ws", Keys: []string{"family-b"}, Paths: []string{"/workspace/b"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.mutation(ctx, "review.run", a, nil) }()
	select {
	case <-admitted:
	case <-time.After(3 * time.Second):
		t.Fatal("mutation not admitted")
	}
	cancel()
	callErr := <-done
	if !errors.Is(callErr, context.Canceled) {
		t.Fatalf("expected cancellation: %v", callErr)
	}
	for range 141 {
		if err := c.mutation(t.Context(), "environment.bind", b, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	for _, conflict := range []environmentMutationScope{a, {WorkspaceID: "ws", Keys: []string{"family-a"}, Paths: []string{"/another/checkout"}}, {WorkspaceID: "ws", Paths: []string{"/workspace/a/child"}}} {
		var uncertain *environmentUncertainOperation
		if err := c.mutation(t.Context(), "environment.bind", conflict, nil); !errors.As(err, &uncertain) {
			t.Fatalf("expected uncertain operation: %v", err)
		}
	}
	if writes.Load() != int32(142) {
		t.Fatalf("unexpected value, want int32(142): %v", writes.Load())
	}
	record, err := runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pending, completed := 0, 0
	for _, receipt := range record.Operations {
		if receipt.State == "pending" {
			pending++
		} else {
			completed++
		}
	}
	if pending != 1 {
		t.Fatalf("unexpected value, want 1: %v", pending)
	}
	if completed > 1 {
		t.Fatalf("exceeded bound: %v", completed)
	}
}

func TestEnvironmentCancellationBeforeDispatchCreatesNoPending(t *testing.T) {
	c, _, _ := newEnvironmentClientForService(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.mutation(ctx, "environment.bind", struct{}{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected true: %v", err)
	}
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 0 {
		t.Fatalf("expected zero: %v", pending)
	}
	record, err := runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(record.Operations) != 0 {
		t.Fatalf("expected empty: %v", len(record.Operations))
	}
}
