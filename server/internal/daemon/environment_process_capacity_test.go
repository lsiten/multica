package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestEnvironmentCapacityReclaimsKnownConsumedReceiptsBeforeAdmission(t *testing.T) {
	var calls atomic.Int32
	c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.Handler = func(context.Context, runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			calls.Add(1)
			return json.RawMessage(`{}`), nil
		}
	})
	ids := make([]string, 0, 128)
	for range 128 {
		status, err := c.process.Client.Health(t.Context())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		request, err := c.process.Client.Request("environment.bind", status.Fence, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		response, err := c.process.Client.Call(t.Context(), request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if response.Receipt.State != "completed" {
			t.Fatalf("unexpected value, want completed: %v", response.Receipt.State)
		}
		ids = append(ids, request.RequestID)
	}
	status, err := c.process.Client.Health(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Receipts.Completed != 128 {
		t.Fatalf("unexpected value, want 128: %v", status.Receipts.Completed)
	}
	c.mu.Lock()
	c.consumed = make(map[string]struct{}, len(ids))
	for _, id := range ids {
		c.consumed[id] = struct{}{}
	}
	c.mu.Unlock()
	if err := c.mutation(t.Context(), "environment.bind", struct{}{}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls.Load() != int32(129) {
		t.Fatalf("unexpected value, want int32(129): %v", calls.Load())
	}
	record, err := runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(record.Operations) > 1 {
		t.Fatalf("exceeded bound: %v", len(record.Operations))
	}
}

func TestEnvironmentCapacityWithOnlyPendingReceiptsReturnsBoundedActionableError(t *testing.T) {
	// The real transport's byte reservation can fill before the receipt-count cap.
	// Exercise the count-cap boundary with authenticated protocol responses.
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: "test", Account: "account", Profile: "profile", DaemonID: "daemon", Service: "environment"}, "fixture/commit")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var mutations, acknowledgements atomic.Int32
	fence := runtimeproc.Fence{SupervisorEpoch: 1, ResourceEpoch: 1, Revision: 1}
	status := runtimeproc.Status{Identity: identity, State: "ready", ReplayEpoch: 1, Fence: fence, Receipts: runtimeproc.ReceiptUsage{Total: 128, Pending: 128, Limit: 128}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request runtimeproc.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		response := runtimeproc.Response{Status: status}
		switch request.Operation {
		case "health", "handshake":
		case "environment.mutation_scope":
			response.Receipt = &runtimeproc.Receipt{RequestID: request.RequestID, State: "read", Result: json.RawMessage(`{"global":true}`)}
		case "query_operation":
			var payload struct {
				RequestID string `json:"request_id"`
			}
			if err := json.Unmarshal(request.Payload, &payload); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			response.Receipt = &runtimeproc.Receipt{RequestID: payload.RequestID, Operation: "review.run", State: "pending"}
		case "acknowledge":
			acknowledgements.Add(1)
			response.Error = &runtimeproc.Error{Code: "unexpected_ack", Message: "pending receipts cannot be retired"}
		default:
			mutations.Add(1)
			response.Error = &runtimeproc.Error{Code: "resource_exhausted", Message: "receipts total=128 pending=128 completed=0; request 1:pending-fixture requires reconciliation"}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	client, err := runtimeproc.NewClient(runtimeproc.Record{Identity: identity, Token: strings.Repeat("a", 64), Address: server.URL, ReplayEpoch: 1, Fence: fence})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := &environmentProcessClient{process: &runtimeproc.Process{Client: client}, gate: make(chan struct{}, 1), confirmations: make(chan struct{}, 4)}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	start := time.Now()
	err = c.mutation(ctx, "environment.bind", struct{}{}, nil)
	if err == nil {
		t.Fatalf("expected error: %v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected deadline: %v", err)
	}
	if time.Since(start) >= 2500*time.Millisecond {
		t.Fatalf("exceeded time bound: %v", time.Since(start))
	}
	if !strings.Contains(err.Error(), "128") {
		t.Fatalf("missing diagnostic 128: %v", err.Error())
	}
	if !strings.Contains(err.Error(), "pending") {
		t.Fatalf("missing diagnostic pending: %v", err.Error())
	}
	if !strings.Contains(err.Error(), "1:pending-fixture") {
		t.Fatalf("missing diagnostic 1:pending-fixture: %v", err.Error())
	}
	if acknowledgements.Load() != 0 {
		t.Fatalf("expected zero: %v", acknowledgements.Load())
	}
	if mutations.Load() > int32(45) {
		t.Fatalf("exceeded bound: %v", mutations.Load())
	}
	cancelled, cancelWait := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancelWait()
	if err := c.mutation(cancelled, "environment.bind", struct{}{}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capacity wait lost caller deadline: %v", err)
	}
	c.mu.Lock()
	tracked := len(c.pending)
	c.mu.Unlock()
	if tracked != 0 || acknowledgements.Load() != 0 {
		t.Fatalf("rejected operation created recovery state: pending=%d acknowledgements=%d", tracked, acknowledgements.Load())
	}
}
