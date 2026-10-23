package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestEnvironmentCompletedFailuresRetireReceipts(t *testing.T) {
	for _, failure := range []string{"domain", "decode"} {
		t.Run(failure, func(t *testing.T) {
			// Given
			c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
				config.Handler = func(context.Context, runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
					if failure == "domain" {
						return nil, &runtimeproc.Error{Code: "domain_failure", Message: "fixture failure"}
					}
					return json.RawMessage(`[]`), nil
				}
			})

			// When
			for attempt := range 140 {
				var output struct{ Value string }
				err := c.mutation(t.Context(), "environment.bind", struct{}{}, &output)
				if failure == "domain" {
					var domainError *runtimeproc.Error
					if !errors.As(err, &domainError) || domainError.Code != "domain_failure" {
						t.Fatalf("attempt %d lost domain error: %v", attempt, err)
					}
				} else {
					var decodeError *json.UnmarshalTypeError
					if !errors.As(err, &decodeError) {
						t.Fatalf("attempt %d lost decode error: %v", attempt, err)
					}
				}
			}

			// Then
			record, err := runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
			if err != nil {
				t.Fatal(err)
			}
			if len(record.Operations) > 1 {
				t.Fatalf("completed failures retained %d receipts", len(record.Operations))
			}
		})
	}
}

func TestEnvironmentConfirmationUnblocksOrdinaryMutationAndRetiresReceipts(t *testing.T) {
	// Given
	ordinaryStarted := make(chan struct{}, 1)
	confirmed := make(chan struct{}, 1)
	c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.Capabilities = []string{"physical.finish_begin", "physical_finish_confirm"}
		config.ConcurrentCapabilities = []string{"physical_finish_confirm"}
		config.Handler = func(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			if request.Operation == "physical_finish_confirm" {
				confirmed <- struct{}{}
				return json.RawMessage(`{}`), nil
			}
			ordinaryStarted <- struct{}{}
			select {
			case <-confirmed:
				return json.RawMessage(`{}`), &runtimeproc.Error{Code: "domain_failure", Message: "fixture failure"}
			case <-ctx.Done():
				return nil, nil
			}
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// When
	for attempt := range 70 {
		ordinaryErr := make(chan error, 1)
		go func() { ordinaryErr <- c.mutation(ctx, "physical.finish_begin", struct{}{}, nil) }()
		select {
		case <-ordinaryStarted:
		case <-ctx.Done():
			t.Fatalf("attempt %d: ordinary mutation did not start: %v", attempt, ctx.Err())
		}
		if err := c.mutation(ctx, "physical_finish_confirm", struct{}{}, nil); err != nil {
			t.Fatalf("attempt %d: confirmation failed: %v", attempt, err)
		}
		var domainError *runtimeproc.Error
		if err := <-ordinaryErr; !errors.As(err, &domainError) || domainError.Code != "domain_failure" {
			t.Fatalf("attempt %d: ordinary mutation lost domain error: %v", attempt, err)
		}
	}

	// Then
	record, err := runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Operations) > 1 {
		t.Fatalf("settled mutation and confirmation retained %d receipts", len(record.Operations))
	}
}

func TestEnvironmentLostAcknowledgementPreservesSuccessAndReconcilesBeforeNextMutation(t *testing.T) {
	// Given
	var mutations, queries, acknowledgements atomic.Int32
	c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.Handler = func(context.Context, runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			mutations.Add(1)
			return json.RawMessage(`{}`), nil
		}
	})
	record, err := runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	upstream := record.Address
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var request runtimeproc.Request
		if err := json.Unmarshal(body, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		forward, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstream+"/rpc", bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		forward.Header = r.Header.Clone()
		response, err := http.DefaultClient.Do(forward)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		if request.Operation == "query_operation" {
			queries.Add(1)
		}
		if request.Operation == "acknowledge" && acknowledgements.Add(1) == 1 {
			// The service committed the ACK, but its response is lost in transit.
			_, _ = io.Copy(io.Discard, response.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"`))
			return
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	t.Cleanup(proxy.Close)
	record.Address = proxy.URL
	c.mu.Lock()
	c.process.Client, err = runtimeproc.NewClient(record)
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	// When
	for attempt := range 2 {
		if err := c.mutation(t.Context(), "environment.bind", struct{}{}, nil); err != nil {
			t.Fatalf("attempt %d: consumed success was replaced by ACK error: %v", attempt, err)
		}
	}

	// Then
	if mutations.Load() != 2 || queries.Load() != 1 || acknowledgements.Load() != 2 {
		t.Fatalf("ACK recovery replayed operations: mutations=%d queries=%d acknowledgements=%d", mutations.Load(), queries.Load(), acknowledgements.Load())
	}
	record, err = runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Operations) > 1 || record.ReplayEpoch != 3 {
		t.Fatalf("ACK recovery left unbounded receipts or advanced replay twice: receipts=%d epoch=%d", len(record.Operations), record.ReplayEpoch)
	}
}

type cancelAfterReceiptDecode struct {
	cancel context.CancelFunc
}

func (output cancelAfterReceiptDecode) UnmarshalJSON([]byte) error {
	output.cancel()
	return nil
}

func TestEnvironmentConsumedReceiptRetiresAfterCallerCancellation(t *testing.T) {
	c, _, _ := newEnvironmentClientForService(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := c.mutation(ctx, "environment.bind", struct{}{}, &cancelAfterReceiptDecode{cancel: cancel}); err != nil {
		t.Fatalf("consumed result changed after cancellation: %v", err)
	}
	record, err := runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if record.ReplayEpoch != 2 || len(record.Operations) != 1 || c.retirementErr != nil {
		t.Fatalf("caller cancellation prevented retirement: epoch=%d receipts=%d error=%v", record.ReplayEpoch, len(record.Operations), c.retirementErr)
	}
}
