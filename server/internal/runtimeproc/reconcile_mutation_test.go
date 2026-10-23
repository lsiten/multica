package runtimeproc

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCanceledMutationRequiresExplicitReconciledOutcome(t *testing.T) {
	for _, mode := range []string{"ordinary_completion", "confirmed", "uncertain", "invalid_json", "oversized_result", "oversized_error", "root_canceled", "reconcile_root_canceled", "reconcile_timeout"} {
		t.Run(mode, func(t *testing.T) {
			b := testBootstrap(t)
			entered := make(chan struct{})
			var reconciled atomic.Int32
			var rootCancel context.CancelFunc
			s, c, cancel, _ := startService(t, Config{Bootstrap: b, DetachMutations: true, Capabilities: []string{"work"}, Handler: func(ctx context.Context, _ Request) (json.RawMessage, *Error) {
				close(entered)
				if mode != "ordinary_completion" {
					<-ctx.Done()
				}
				return json.RawMessage(`{"provisional":true}`), nil
			}, ReconcileMutation: func(ctx context.Context, req Request, prior Outcome) (Outcome, bool) {
				reconciled.Add(1)
				if req.Operation != "work" || string(prior.Result) != `{"provisional":true}` {
					t.Error("lost original request/outcome")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 2*time.Second {
					t.Error("unbounded reconciliation")
				}
				switch mode {
				case "uncertain":
					return Outcome{}, false
				case "invalid_json":
					return Outcome{Result: json.RawMessage(`{`)}, true
				case "oversized_result":
					return Outcome{Result: json.RawMessage(`"` + strings.Repeat("x", maxBody) + `"`)}, true
				case "oversized_error":
					return Outcome{Error: &Error{Code: "failure", Message: strings.Repeat("x", 1025)}}, true
				case "reconcile_root_canceled":
					rootCancel()
					<-ctx.Done()
				case "reconcile_timeout":
					<-ctx.Done()
				}
				return Outcome{Result: json.RawMessage(`{"confirmed":true}`)}, true
			}})
			rootCancel = cancel
			req := request(t, c, "work", "")
			req.Deadline = time.Now().Add(50 * time.Millisecond)
			if mode == "root_canceled" {
				req.Deadline = time.Now().Add(time.Second)
			}
			done := make(chan struct{})
			go func() { _, _ = c.Call(t.Context(), req); close(done) }()
			<-entered
			if mode == "root_canceled" {
				cancel()
			}
			<-done
			// Lifecycle admission waits for the inline handler and reconciliation to join.
			ctx, stop := context.WithTimeout(t.Context(), 3*time.Second)
			defer stop()
			release, err := s.admit(ctx, "acknowledge")
			if err != nil {
				t.Fatal(err)
			}
			release()
			s.mu.Lock()
			receipt := s.record.Operations[req.RequestID]
			s.mu.Unlock()
			want := "pending"
			if mode == "confirmed" || mode == "ordinary_completion" {
				want = "completed"
			}
			if receipt.State != want {
				t.Fatalf("receipt=%+v want=%s", receipt, want)
			}
			if mode == "root_canceled" || mode == "ordinary_completion" {
				if reconciled.Load() != 0 {
					t.Fatal("reconciled after root cancellation")
				}
			} else if reconciled.Load() != 1 {
				t.Fatal("reconciliation not invoked once")
			}
			if mode == "confirmed" {
				disk, err := ReadRecord(b.Root, b.Identity)
				if err != nil || string(disk.Operations[req.RequestID].Result) != `{"confirmed":true}` {
					t.Fatalf("outcome not persisted: %+v %v", disk, err)
				}
			}
		})
	}
}

func TestDetachedAcknowledgementExecutionContext(t *testing.T) {
	for _, operation := range []string{"acknowledge", "work", "stop", "drain", "resume"} {
		t.Run(operation, func(t *testing.T) {
			caller, disconnect := context.WithCancel(t.Context())
			disconnect()
			root, shutdown := context.WithCancel(t.Context())
			defer shutdown()
			req := Request{Operation: operation, Deadline: time.Now().Add(time.Minute)}
			service := Service{config: Config{DetachMutations: true}}
			ctx, cancel := service.executionContext(caller, root, req)
			defer cancel()
			detached := operation == "acknowledge" || operation == "work"
			if (ctx.Err() == nil) != detached {
				t.Fatalf("incorrect caller cancellation: %s %v", operation, ctx.Err())
			}
			if detached {
				deadline, ok := ctx.Deadline()
				if !ok || deadline != req.Deadline {
					t.Fatal("original deadline lost")
				}
			}
			shutdown()
			if ctx.Err() == nil {
				t.Fatal("root cancellation lost")
			}
		})
	}
}
