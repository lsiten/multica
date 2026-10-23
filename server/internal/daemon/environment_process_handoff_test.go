package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnvironmentCanceledPreparationSettlesBeforeRetirement(t *testing.T) {
	for _, mode := range []string{"prepare", "begin_task", "lost_settlement_response", "decode_failure"} {
		t.Run(mode, func(t *testing.T) {
			operation := "physical.prepare"
			var recoveryCalls atomic.Int32
			if mode == "begin_task" {
				operation = "physical.begin_task"
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			owner, err := newEnvironmentPhysicalOwner(t.TempDir(), t.TempDir(), logger)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(owner.close)
			entered, release := make(chan struct{}), make(chan struct{})
			prepared := make(chan environmentPhysicalResult, 1)
			input := execenv.PhysicalPrepareParams{WorkspaceID: "ws", TaskID: "task", AgentName: "fixture"}
			c, _, _ := newEnvironmentClientForService(t, func(config *runtimeproc.Config) {
				config.DetachMutations = true
				config.Capabilities = append(config.Capabilities, "physical.prepare", "physical.begin_task", "physical.recover_handoff")
				config.Handler = func(ctx context.Context, req runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
					if req.Operation == "physical.recover_handoff" {
						recoveryCalls.Add(1)
						var recovery environmentHandoffRecovery
						if err := json.Unmarshal(req.Payload, &recovery); err != nil {
							return environmentOperationResult(nil, err)
						}
						result, err := owner.recoverUnclaimed(ctx, recovery)
						return environmentOperationResult(result, err)
					}
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return nil, nil
					}
					result, err := owner.prepare(ctx, input)
					prepared <- result
					return environmentOperationResult(result, err)
				}
			})
			if mode == "lost_settlement_response" {
				record, err := runtimeproc.ReadRecord(c.bootstrap.Root, c.bootstrap.Identity)
				if err != nil {
					t.Fatal(err)
				}
				upstream := record.Address
				var lost atomic.Bool
				proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					var req runtimeproc.Request
					if err = json.Unmarshal(body, &req); err != nil {
						t.Error(err)
						return
					}
					forward, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstream+"/rpc", bytes.NewReader(body))
					if err != nil {
						t.Error(err)
						return
					}
					forward.Header = r.Header.Clone()
					response, err := http.DefaultClient.Do(forward)
					if err != nil {
						return
					}
					defer response.Body.Close()
					if req.Operation == "physical.recover_handoff" && lost.CompareAndSwap(false, true) {
						_, _ = io.Copy(io.Discard, response.Body)
						_, _ = w.Write([]byte(`{"`))
						return
					}
					w.WriteHeader(response.StatusCode)
					_, _ = io.Copy(w, response.Body)
				}))
				t.Cleanup(proxy.Close)
				record.Address = proxy.URL
				c.process.Client, err = runtimeproc.NewClient(record)
				if err != nil {
					t.Fatal(err)
				}
			}
			var payload any = input
			if operation == "physical.begin_task" {
				payload = struct {
					Physical execenv.PhysicalPrepareParams `json:"physical"`
				}{input}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var result environmentPhysicalResult
				var output any = &result
				if mode == "decode_failure" {
					output = &struct {
						Reservation string `json:"reservation"`
					}{}
				}
				done <- c.mutation(ctx, operation, payload, output)
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("allocation not admitted")
			}
			if mode != "decode_failure" {
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("caller cancellation=%v", err)
				}
			}
			close(release)
			if mode == "decode_failure" {
				var decode *json.UnmarshalTypeError
				if err := <-done; !errors.As(err, &decode) {
					t.Fatalf("result decode failure=%v", err)
				}
			}
			var result environmentPhysicalResult
			select {
			case result = <-prepared:
			case <-time.After(3 * time.Second):
				t.Fatal("allocation did not complete")
			}
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				c.mu.Lock()
				settled := len(c.pending) == 0 && len(c.consumed) == 0 && c.acknowledgement == nil
				c.mu.Unlock()
				if settled {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			c.mu.Lock()
			pending, consumed := len(c.pending), len(c.consumed)
			ack := c.acknowledgement != nil
			c.mu.Unlock()
			status, healthErr := c.process.Client.Health(t.Context())
			reserved, reservationErr := execenv.PhysicalRootReserved(result.Reservation.RootDir)
			_, nextErr := owner.prepare(t.Context(), input)
			t.Logf("after background ACK: pending=%d consumed=%d ACK=%v receipts=%d; unclaimed root reserved=%v reservation=%v retry=%v health=%v", pending, consumed, ack, status.Receipts.Total, reserved, reservationErr, nextErr, healthErr)
			if pending != 0 || consumed != 0 || ack || reserved || nextErr != nil {
				t.Error("unclaimed allocation was not settled before receipt retirement")
			}
			if status.Receipts.Total > 1 {
				t.Errorf("settlement receipts accumulated: %+v", status.Receipts)
			}
			if recoveryCalls.Load() != 1 {
				t.Fatalf("handoff recovery replayed: %d calls", recoveryCalls.Load())
			}
		})
	}
}
