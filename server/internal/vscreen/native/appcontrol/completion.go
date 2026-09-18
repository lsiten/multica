package appcontrol

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// PIDCompletionBinding is native-host authority for one posted action, never tool input.
type PIDCompletionBinding struct {
	Token    uint64
	Target   protocol.VscreenActionTarget
	ActionID string
	Sequence uint64
	Process  Process
}

// PIDCompletion supplies the code-owned verifier with the original operation and target.
type PIDCompletion struct {
	Binding PIDCompletionBinding
	Action  protocol.VscreenAction
	Window  Window
}

// PIDCompletionReceipt must attest final target processing and independently checked effect.
type PIDCompletionReceipt struct {
	Binding         PIDCompletionBinding
	TargetProcessed bool
	EffectVerified  bool
}

type nativePIDCompletion struct {
	Token    string
	Target   protocol.VscreenActionTarget
	ActionID string
	Sequence uint64
	Process  Process
	WindowID uint32
}

func newPIDCompletion(request protocol.VscreenActionRequest, window Window) (PIDCompletion, error) {
	var bytes [8]byte
	for {
		if _, err := rand.Read(bytes[:]); err != nil {
			return PIDCompletion{}, err
		}
		if binary.BigEndian.Uint64(bytes[:]) != 0 {
			break
		}
	}
	return PIDCompletion{Binding: PIDCompletionBinding{Token: binary.BigEndian.Uint64(bytes[:]), Target: request.Target, ActionID: request.ActionID, Sequence: request.Sequence, Process: window.Process}, Action: request.Action, Window: window}, nil
}
func (p PIDCompletion) native() nativePIDCompletion {
	return nativePIDCompletion{Token: fmt.Sprintf("%016x", p.Binding.Token), Target: p.Binding.Target, ActionID: p.Binding.ActionID, Sequence: p.Binding.Sequence, Process: p.Binding.Process, WindowID: p.Window.WindowID}
}
func (c *Controller) verifyPIDCompletion(ctx context.Context, a Authority, completion PIDCompletion, d Display) error {
	if c.config.VerifyPIDCompletion == nil {
		return refusal("action_uncertain")
	}
	receipt, err := c.config.VerifyPIDCompletion(ctx, completion)
	if err != nil {
		return refusal("action_uncertain")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if receipt.Binding != completion.Binding || !receipt.TargetProcessed || !receipt.EffectVerified {
		return refusal("action_uncertain")
	}
	live, err := c.authorize(ctx, a, ControlAccess)
	if err != nil {
		return err
	}
	if live != d {
		return refusal("stale_authority")
	}
	return c.backend.call(ctx, "pid_complete", map[string]any{"Completion": completion.native(), "Display": d}, nil)
}
