package appcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func syntheticPIDCompletion(_ context.Context, c PIDCompletion) (PIDCompletionReceipt, error) {
	return PIDCompletionReceipt{Binding: c.Binding, TargetProcessed: true, EffectVerified: true}, nil
}

func TestPIDCertificateWithoutCompletionCannotDispatchFallback(t *testing.T) {
	for _, mechanism := range []string{"ax", "pid"} {
		t.Run(mechanism, func(t *testing.T) {
			c, b, _, request := controlFixture(t)
			c.config.CertifiedPIDInput = func(Process, protocol.VscreenAction) PIDInputDecision { return PIDInputDecision{Certified: true} }
			b.run = func(_ context.Context, op string, in, out any) error {
				if op == "action" {
					if in.(map[string]any)["CertifiedPID"].(bool) {
						t.Fatal("certificate without verifier enabled PID")
					}
					if mechanism == "pid" {
						return refusal("needs_intervention")
					}
					*out.(*Result) = Result{Outcome: protocol.VscreenActionVerified, Mechanism: "ax"}
				}
				return nil
			}
			result, err := c.Act(t.Context(), request)
			if mechanism == "ax" && (err != nil || result.Mechanism != "ax" || result.CompletionVerified) {
				t.Fatalf("AX changed: %+v %v", result, err)
			}
			if mechanism == "pid" && err == nil {
				t.Fatal("unverified PID accepted")
			}
		})
	}
}

func TestPIDCompletionRequiresExactReceiptAndKeepsUnknownFence(t *testing.T) {
	for _, tc := range []string{"valid", "token", "resource", "task", "transaction", "lease", "process", "window", "epoch", "snapshot", "action", "sequence", "not_processed", "no_effect", "cancel", "authority", "clear_error"} {
		t.Run(tc, func(t *testing.T) {
			c, b, a, request := controlFixture(t)
			request.Action = protocol.VscreenAction{Kind: protocol.VscreenActionKey, Key: &protocol.VscreenKeyAction{Key: "ArrowLeft"}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			c.config.CertifiedPIDInput = func(Process, protocol.VscreenAction) PIDInputDecision {
				return PIDInputDecision{Certified: true, InputSourceID: "layout"}
			}
			pending := false
			clears := 0
			var dispatched nativePIDCompletion
			b.run = func(_ context.Context, op string, in, out any) error {
				switch op {
				case "pid_identity":
					*out.(*Process) = c.windows["owned"].window.Process
				case "action":
					dispatched = in.(map[string]any)["Completion"].(nativePIDCompletion)
					if len(dispatched.Token) != 16 || dispatched.Token == strings.Repeat("0", 16) {
						t.Fatal("invalid one-use token")
					}
					pending = true
					*out.(*Result) = Result{Outcome: protocol.VscreenActionDispatched, Mechanism: "pid"}
				case "pid_complete":
					clears++
					if tc == "clear_error" {
						return refusal("action_uncertain")
					}
					if in.(map[string]any)["Completion"] != dispatched {
						t.Fatal("clear binding differs")
					}
					pending = false
				case "quiesce":
					if pending {
						return refusal("action_uncertain")
					}
				}
				return nil
			}
			c.config.VerifyPIDCompletion = func(_ context.Context, completion PIDCompletion) (PIDCompletionReceipt, error) {
				if !pending || completion.Binding.Target != request.Target || completion.Binding.Token == 0 || completion.Window.Handle != "owned" {
					t.Fatal("missing original scoped operation")
				}
				receipt, _ := syntheticPIDCompletion(ctx, completion)
				switch tc {
				case "token":
					receipt.Binding.Token++
				case "resource":
					receipt.Binding.Target.Resource.RuntimeID = "other"
				case "task":
					receipt.Binding.Target.TaskID = "other"
				case "transaction":
					receipt.Binding.Target.TransactionID = "other"
				case "lease":
					receipt.Binding.Target.LeaseEpoch++
				case "process":
					receipt.Binding.Process.Start = "reused"
				case "window":
					receipt.Binding.Target.WindowHandle = "other"
				case "epoch":
					receipt.Binding.Target.Epoch.GeometryRevision++
				case "snapshot":
					receipt.Binding.Target.SnapshotRevision++
				case "action":
					receipt.Binding.ActionID = "other"
				case "sequence":
					receipt.Binding.Sequence++
				case "not_processed":
					receipt.TargetProcessed = false
				case "no_effect":
					receipt.EffectVerified = false
				case "cancel":
					cancel()
				case "authority":
					c.config.Authorize = func(context.Context, Authority, Access) (Display, error) {
						return Display{}, refusal("stale_authority")
					}
				}
				return receipt, nil
			}
			result, err := c.Act(ctx, request)
			if tc == "valid" {
				if err != nil || result.Outcome != protocol.VscreenActionVerified || result.Mechanism != "pid" || !result.CompletionVerified || pending || clears != 1 {
					t.Fatalf("completion=%+v err=%v pending=%v clears=%d", result, err, pending, clears)
				}
				raw, _ := json.Marshal(result)
				if strings.Contains(string(raw), dispatched.Token) {
					t.Fatal("private token leaked in result")
				}
				_, err = c.Act(t.Context(), request)
				if err != nil || clears != 1 {
					t.Fatal("cached retry repeated completion")
				}
			} else {
				if err == nil || !pending || result.CompletionVerified {
					t.Fatalf("unknown receipt cleared fence: %+v %v", result, err)
				}
				if tc != "clear_error" && clears != 0 {
					t.Fatal("bad receipt reached clear backend")
				}
				if c.Quiesce(t.Context(), a.Resource) == nil {
					t.Fatal("unknown action became quiescent")
				}
				if c.Resume(t.Context(), a) == nil {
					t.Fatal("pending action resumed")
				}
				if _, e := c.HumanTransfer(t.Context(), HumanRequest{Grant: "local-one-use", Resource: a.Resource, WindowHandle: "owned", Direction: "to_real"}); e == nil {
					t.Fatal("pending action transferred")
				}
				if c.Dispose(t.Context(), a.Resource) == nil {
					t.Fatal("pending action disposed/released claim")
				}
				for _, op := range b.calls {
					if op == "move" || op == "restore" || op == "forget" {
						t.Fatalf("pending action reached %s", op)
					}
				}
			}
		})
	}
}
func TestPIDCompletionVerifierErrorCannotClear(t *testing.T) {
	c, b, _, r := controlFixture(t)
	c.config.CertifiedPIDInput = func(Process, protocol.VscreenAction) PIDInputDecision { return PIDInputDecision{Certified: true} }
	c.config.VerifyPIDCompletion = func(context.Context, PIDCompletion) (PIDCompletionReceipt, error) {
		return PIDCompletionReceipt{}, errors.New("effect_unconfirmed")
	}
	b.run = func(_ context.Context, op string, in, out any) error {
		if op == "action" {
			*out.(*Result) = Result{Outcome: protocol.VscreenActionDispatched, Mechanism: "pid"}
		}
		if op == "pid_complete" {
			t.Fatal("failed verifier cleared")
		}
		return nil
	}
	if _, err := c.Act(t.Context(), r); err == nil {
		t.Fatal("failed verifier accepted")
	}
}

func TestPIDCapabilityDoesNotAdvertiseVariantsWithoutCompletion(t *testing.T) {
	c, b, a, _ := controlFixture(t)
	c.config.CertifiedPIDInput = func(Process, protocol.VscreenAction) PIDInputDecision { return PIDInputDecision{Certified: true} }
	c.config.PIDInputVerification = func(Process) string { return "verified_variants" }
	b.run = func(_ context.Context, op string, in, out any) error {
		if op == "observe" {
			*out.(*Observation) = Observation{Window: c.windows["owned"].window, Width: 100, Height: 100}
		}
		return nil
	}
	observed, err := c.Observe(t.Context(), a, "owned", false)
	if err != nil || observed.PIDInputCompletionAvailable || observed.PIDInputVerification != "none" {
		t.Fatalf("unusable mechanism advertised: %+v %v", observed, err)
	}
	c.config.VerifyPIDCompletion = syntheticPIDCompletion
	observed, err = c.Observe(t.Context(), a, "owned", false)
	if err != nil || !observed.PIDInputCompletionAvailable || observed.PIDInputVerification != "verified_variants" {
		t.Fatalf("complete wiring hidden: %+v %v", observed, err)
	}
}

func TestPIDConfirmedCompletionThenCancellationDoesNotReplayOrReviveAuthority(t *testing.T) {
	c, b, a, request := controlFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c.config.CertifiedPIDInput = func(Process, protocol.VscreenAction) PIDInputDecision { return PIDInputDecision{Certified: true} }
	c.config.VerifyPIDCompletion = syntheticPIDCompletion
	pending := false
	actions := 0
	clears := 0
	b.run = func(_ context.Context, op string, in, out any) error {
		switch op {
		case "action":
			actions++
			pending = true
			*out.(*Result) = Result{Outcome: protocol.VscreenActionDispatched, Mechanism: "pid"}
		case "pid_complete":
			clears++
			pending = false
			cancel()
			c.config.Authorize = func(context.Context, Authority, Access) (Display, error) {
				return Display{}, refusal("stale_authority")
			}
		case "quiesce":
			if pending {
				return refusal("action_uncertain")
			}
		}
		return nil
	}
	result, err := c.Act(ctx, request)
	if err == nil || result.Outcome != protocol.VscreenActionUncertain || !result.CompletionVerified || pending || clears != 1 {
		t.Fatalf("completion facts/cancel state lost: %+v %v", result, err)
	}
	if err = c.Quiesce(t.Context(), a.Resource); err != nil {
		t.Fatalf("confirmed completion became unknown: %v", err)
	}
	if _, err = c.Act(t.Context(), request); err == nil || actions != 1 {
		t.Fatal("cancelled old action replayed")
	}
	if c.Resume(t.Context(), a) == nil {
		t.Fatal("old authority revived")
	}
}
