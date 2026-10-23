package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestRuntimeClientsConsumeCompletedErrors(t *testing.T) {
	for _, kind := range []string{"worker", "gateway", "application"} {
		for _, refusal := range []bool{false, true} {
			name := "decode"
			if refusal {
				name = "domain"
			}
			t.Run(kind+"/"+name, func(t *testing.T) {
				operation := kind + ".bind"
				env, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
					config.Capabilities = []string{operation}
					config.Handler = func(context.Context, runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
						if refusal {
							return json.RawMessage(`[]`), &runtimeproc.Error{Code: "not_launchable", Message: "fixture refusal"}
						}
						return json.RawMessage(`[]`), nil
					}
				})
				var mutate func(context.Context, string, any, any) error
				switch kind {
				case "worker":
					mutate = (&workerProcessClient{transport: env.process.Client, gate: make(chan struct{}, 1)}).mutation
				case "gateway":
					mutate = (&gatewayProcessClient{process: env.process, gate: make(chan struct{}, 1)}).mutation
				case "application":
					mutate = (&applicationProcessClient{process: env.process, gate: make(chan struct{}, 1)}).mutation
				}
				for i := range 129 {
					var output struct{ Value string }
					err := mutate(t.Context(), operation, struct{}{}, &output)
					if refusal {
						var domain *runtimeproc.Error
						if !errors.As(err, &domain) || domain.Code != "not_launchable" || domain.Message != "fixture refusal" {
							t.Fatalf("operation %d lost refusal: %v", i, err)
						}
					} else {
						var decode *json.UnmarshalTypeError
						if !errors.As(err, &decode) {
							t.Fatalf("operation %d lost decode error: %v", i, err)
						}
					}
				}
				status, err := env.process.Client.Health(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if status.Receipts.Completed > 1 || status.Receipts.Pending != 0 {
					t.Fatalf("unconsumed receipts: %+v", status.Receipts)
				}
			})
		}
	}
}

func TestWorkerRunReturnsCompletedErrorWithoutPolling(t *testing.T) {
	for _, refusal := range []bool{false, true} {
		name := "decode"
		if refusal {
			name = "domain"
		}
		t.Run(name, func(t *testing.T) {
			env, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
				config.Capabilities = []string{"worker.run"}
				config.Handler = func(context.Context, runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
					if refusal {
						return nil, &runtimeproc.Error{Code: "not_launchable", Message: "fixture refusal"}
					}
					return json.RawMessage(`[]`), nil
				}
			})
			c := &workerProcessClient{transport: env.process.Client, gate: make(chan struct{}, 1)}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			result, err := c.run(ctx)
			if result != nil || err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected immediate completed failure, got %v, %v", result, err)
			}
			if refusal {
				var domain *runtimeproc.Error
				if !errors.As(err, &domain) || domain.Code != "not_launchable" {
					t.Fatalf("lost refusal: %v", err)
				}
			} else {
				var decode *json.UnmarshalTypeError
				if !errors.As(err, &decode) {
					t.Fatalf("lost decode error: %v", err)
				}
			}
		})
	}
}

func TestRuntimeClientsLocalRejectionAllowsNextMutation(t *testing.T) {
	for _, kind := range []string{"worker", "gateway", "application"} {
		t.Run(kind, func(t *testing.T) {
			operation := kind + ".bind"
			env, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
				config.Capabilities = []string{operation}
				config.Handler = func(context.Context, runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
					return json.RawMessage(`{}`), nil
				}
			})
			var mutate func(context.Context, string, any, any) error
			switch kind {
			case "worker":
				mutate = (&workerProcessClient{transport: env.process.Client, gate: make(chan struct{}, 1)}).mutation
			case "gateway":
				mutate = (&gatewayProcessClient{process: env.process, gate: make(chan struct{}, 1)}).mutation
			case "application":
				mutate = (&applicationProcessClient{process: env.process, gate: make(chan struct{}, 1)}).mutation
			}
			err := mutate(t.Context(), operation, strings.Repeat("x", 2<<20), nil)
			var local *runtimeproc.NotSubmittedError
			if !errors.As(err, &local) {
				t.Fatalf("expected local rejection: %v", err)
			}
			if err := mutate(t.Context(), operation, struct{}{}, nil); err != nil {
				t.Fatalf("local rejection poisoned client: %v", err)
			}
		})
	}
}

type workerAckFailureTransport struct{ *runtimeproc.Client }

func (c workerAckFailureTransport) Call(ctx context.Context, request runtimeproc.Request) (runtimeproc.Response, error) {
	if request.Operation == "acknowledge" {
		return runtimeproc.Response{}, errors.New("lost acknowledgement")
	}
	return c.Client.Call(ctx, request)
}

func TestWorkerRunPreservesRefusalWhenAcknowledgementFails(t *testing.T) {
	env, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.Capabilities = []string{"worker.run"}
		config.Handler = func(context.Context, runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
			return nil, &runtimeproc.Error{Code: "not_launchable", Message: "fixture refusal"}
		}
	})
	c := &workerProcessClient{transport: workerAckFailureTransport{env.process.Client}, gate: make(chan struct{}, 1)}
	_, err := c.run(t.Context())
	var domain *runtimeproc.Error
	if !errors.As(err, &domain) || domain.Code != "not_launchable" {
		t.Fatalf("acknowledgement masked refusal: %v", err)
	}
	if c.uncertain == nil {
		t.Fatal("lost acknowledgement must prevent a fresh mutation")
	}
	status, err := env.process.Client.Health(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Receipts.Completed != 1 {
		t.Fatalf("unacknowledged result disappeared: %+v", status.Receipts)
	}
}

func TestWorkerRunConsumesEmptyCompletedResult(t *testing.T) {
	env, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
		config.Capabilities = []string{"worker.run"}
		config.Handler = func(context.Context, runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) { return nil, nil }
	})
	c := &workerProcessClient{transport: env.process.Client, gate: make(chan struct{}, 1)}
	for i := range 129 {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		result, err := c.run(ctx)
		cancel()
		var decode *json.SyntaxError
		if result != nil || !errors.As(err, &decode) {
			t.Fatalf("operation %d expected empty-result decode failure without polling, got %v, %v", i, result, err)
		}
	}
	status, err := env.process.Client.Health(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Receipts.Completed > 1 || status.Receipts.Pending != 0 {
		t.Fatalf("unconsumed empty receipts: %+v", status.Receipts)
	}
	if err := c.mutation(t.Context(), "worker.run", struct{}{}, nil); err != nil {
		t.Fatalf("operation without expected result must accept empty payload: %v", err)
	}
}
