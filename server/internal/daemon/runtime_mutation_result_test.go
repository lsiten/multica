package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

func TestRuntimeClientsConsumeCompletedErrors(t *testing.T) {
	for _, kind := range []string{"gateway", "application"} {
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

func TestRuntimeClientsLocalRejectionAllowsNextMutation(t *testing.T) {
	for _, kind := range []string{"gateway", "application"} {
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
