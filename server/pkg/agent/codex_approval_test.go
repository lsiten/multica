package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestCodexApprovalWaitsForExplicitDecision(t *testing.T) {
	for _, scenario := range []string{"accept", "decline", "failed", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			c, input, _ := newTestCodexClient(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			c.approvalContext = ctx
			reached := make(chan ApprovalRequest, 1)
			decide := make(chan struct{})
			c.cfg.RequestApproval = func(ctx context.Context, request ApprovalRequest) (bool, error) {
				reached <- request
				select {
				case <-decide:
				case <-ctx.Done():
					return false, ctx.Err()
				}
				if scenario == "failed" {
					return false, errors.New("review unavailable")
				}
				return scenario == "accept", nil
			}
			c.handleLine(`{"jsonrpc":"2.0","id":10,"method":"item/commandExecution/requestApproval","params":{"command":"echo example"}}`)
			select {
			case request := <-reached:
				if request.Method != "item/commandExecution/requestApproval" || string(request.Params) != `{"command":"echo example"}` {
					t.Fatal("request changed")
				}
			case <-time.After(time.Second):
				t.Fatal("reviewer not called")
			}
			if len(input.Lines()) != 0 {
				t.Fatal("approval responded before the reviewer decided")
			}
			if scenario == "cancelled" {
				cancel()
			} else {
				close(decide)
			}
			deadline := time.Now().Add(time.Second)
			for len(input.Lines()) == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			lines := input.Lines()
			if len(lines) != 1 {
				t.Fatalf("responses=%d", len(lines))
			}
			var reply struct {
				ID     int `json:"id"`
				Result struct {
					Decision string `json:"decision"`
				} `json:"result"`
			}
			if err := json.Unmarshal([]byte(lines[0]), &reply); err != nil {
				t.Fatal(err)
			}
			want := "decline"
			if scenario == "accept" {
				want = "accept"
			}
			if reply.ID != 10 || reply.Result.Decision != want {
				t.Fatalf("unexpected decision: %+v", reply)
			}
		})
	}
}
