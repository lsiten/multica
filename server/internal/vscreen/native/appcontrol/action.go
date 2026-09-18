package appcontrol

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type grantBinding struct {
	Resource              protocol.ResourceKey
	Epoch                 protocol.VscreenEpoch
	TaskID, TransactionID string
	LeaseEpoch            uint64
}
type actionIdentity struct {
	Grant grantBinding
	ID    string
}

type actionRecord struct {
	digest [32]byte
	result Result
	err    error
}

// Act rechecks trusted authority before dispatch; identical retries never emit input again.
func (c *Controller) Act(ctx context.Context, request protocol.VscreenActionRequest) (Result, error) {
	if request.Validate() != nil {
		return Result{}, refusal("invalid_action")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return Result{}, err
	}
	var copyRequest protocol.VscreenActionRequest
	if err = json.Unmarshal(raw, &copyRequest); err != nil {
		return Result{}, err
	}
	request = copyRequest
	ctx, leave, err := c.enter(ctx, request.Target.Resource)
	if err != nil {
		return Result{}, err
	}
	defer leave()
	t := request.Target
	a := Authority{Resource: t.Resource, Epoch: t.Epoch, TaskID: t.TaskID, TransactionID: t.TransactionID, LeaseEpoch: t.LeaseEpoch}
	d, err := c.authorize(ctx, a, ControlAccess)
	if err != nil {
		return Result{}, err
	}
	binding := grantBinding{Resource: a.Resource, Epoch: a.Epoch, TaskID: a.TaskID, TransactionID: a.TransactionID, LeaseEpoch: a.LeaseEpoch}
	id := actionIdentity{Grant: binding, ID: request.ActionID}
	digest := sha256.Sum256(raw)
	if cached, ok := c.actions[id]; ok {
		if cached.digest != digest {
			return Result{}, refusal("action_conflict")
		}
		return cached.result, cached.err
	}
	w, err := c.owned(t.WindowHandle, d)
	if err != nil {
		return Result{}, err
	}
	if w.window.SnapshotRevision != t.SnapshotRevision || request.Sequence != c.sequence[binding]+1 || len(c.actions) >= 4096 {
		return Result{}, refusal("stale_snapshot")
	}
	certified := c.config.CertifiedPIDInput != nil && c.config.CertifiedPIDInput(w.window.Process, request.Action)
	var result Result
	c.sequence[binding] = request.Sequence
	err = c.backend.call(ctx, "action", map[string]any{"Window": w.window, "Display": d, "Action": request.Action, "CertifiedPID": certified}, &result)
	if err != nil || ctx.Err() != nil || result.Outcome != protocol.VscreenActionVerified && result.Outcome != protocol.VscreenActionDispatched {
		c.freeze(a.Resource)
		result.Outcome = protocol.VscreenActionUncertain
		if err == nil {
			err = refusal("action_uncertain")
		}
	}
	w.window.SnapshotRevision = 0
	c.actions[id] = actionRecord{digest: digest, result: result, err: err}
	return result, err
}

// Resume is only for a host-verified fresh grant after explicit recovery or transaction handoff.
// Its verifier must reject stale/cancelled task generations, including disconnected parents.
func (c *Controller) Resume(ctx context.Context, a Authority) error {
	if a.Resource.Validate() != nil || a.Epoch.Validate() != nil || a.TaskID == "" || a.TransactionID == "" || a.LeaseEpoch == 0 {
		return refusal("stale_authority")
	}
	ctx, leave, err := c.enter(ctx, a.Resource)
	if err != nil {
		return err
	}
	defer leave()
	d, err := c.config.Authorize(ctx, a, ControlAccess)
	if err != nil {
		return err
	}
	if d.Resource != a.Resource || d.Epoch != a.Epoch || !d.Virtual || a.LeaseEpoch == 0 {
		return refusal("stale_authority")
	}
	if err = c.backend.call(ctx, "quiesce", map[string]any{"Resource": a.Resource}, nil); err != nil {
		return err
	}
	if err = c.backend.call(ctx, "resume", d, nil); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.frozen, a.Resource)
	c.mu.Unlock()
	return nil
}

// HumanTransfer moves only a registered window after the separate local owner grant and barrier.
func (c *Controller) HumanTransfer(ctx context.Context, request HumanRequest) (Window, error) {
	if request.Grant == "" || request.Resource.Validate() != nil || request.Direction != "to_real" && request.Direction != "to_virtual" {
		return Window{}, refusal("human_grant_required")
	}
	ctx, leave, err := c.enter(ctx, request.Resource)
	if err != nil {
		return Window{}, err
	}
	defer leave()
	w := c.windows[request.WindowHandle]
	if w == nil || w.display.Resource != request.Resource {
		return Window{}, refusal("stale_window")
	}
	if err = c.backend.call(ctx, "quiesce", map[string]any{"Resource": request.Resource}, nil); err != nil {
		return Window{}, err
	}
	destination, err := c.config.AuthorizeHuman(ctx, request)
	if err != nil {
		return Window{}, err
	}
	if destination.Resource != request.Resource || destination.Epoch.Validate() != nil || destination.Epoch.NativeEpoch != w.display.Epoch.NativeEpoch || destination.ID == 0 || !destination.Bounds.valid() || destination.Virtual != (request.Direction == "to_virtual") {
		return Window{}, refusal("human_grant_required")
	}
	c.mu.Lock()
	c.frozen[request.Resource] = true
	c.mu.Unlock()
	var moved Window
	if err = c.backend.call(ctx, "move", map[string]any{"Window": w.window, "Display": destination, "Background": false, "Activate": request.Direction == "to_real"}, &moved); err != nil {
		return Window{}, err
	}
	if !validWindowReply(moved, w.window, destination) {
		return Window{}, refusal("stale_window")
	}
	w.window = moved
	w.human = request.Direction == "to_real"
	if !w.human {
		w.display = destination
	}
	return moved, nil
}
