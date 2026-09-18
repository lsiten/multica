package native

import (
	"context"
	"reflect"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/internal/vscreen/smokefixture"
)

func (q *inputQualification) verifyCompletion(ctx context.Context, c appcontrol.PIDCompletion) (appcontrol.PIDCompletionReceipt, error) {
	q.mu.Lock()
	expected, exists := q.pending[c.Binding.ActionID]
	before, window := q.before, q.window
	valid := exists && c.Binding.Token != 0 && c.Binding.Token != before.LastProcessedToken && c.Binding.Target == expected.Target && c.Binding.Sequence == expected.Sequence && c.Binding.Process == window.Process && reflect.DeepEqual(c.Action, expected.Action) && c.Window.Handle == window.Handle && c.Window.WindowID == window.WindowID && c.Window.DisplayID == window.DisplayID && c.Window.Process == window.Process && c.Window.SnapshotRevision == c.Binding.Target.SnapshotRevision && c.Binding.Target.Resource == q.scope.Resource
	q.mu.Unlock()
	if !valid || ctx.Err() != nil || !time.Now().Before(q.expires) || q.validate() != nil {
		return appcontrol.PIDCompletionReceipt{}, appRefusal("action_uncertain")
	}
	defer func() { q.mu.Lock(); delete(q.pending, c.Binding.ActionID); q.mu.Unlock() }()
	var finalType uint32
	switch c.Action.Kind {
	case "click", "drag":
		finalType = 2
	case "type", "key":
		finalType = 11
	case "scroll":
		finalType = 22
	default:
		return appcontrol.PIDCompletionReceipt{}, appRefusal("action_uncertain")
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := q.scope.Read()
		if err == nil && state.LastProcessedToken == c.Binding.Token && state.LastEventType == finalType && state.PID == window.Process.PID && state.ProcessStart == window.Process.Start && state.WindowID == window.WindowID && state.DisplayID == window.DisplayID && !state.Closed && smokefixture.QualificationEffect(string(c.Action.Kind), before, state) {
			if ctx.Err() != nil || !time.Now().Before(q.expires) || q.validate() != nil {
				return appcontrol.PIDCompletionReceipt{}, appRefusal("action_uncertain")
			}
			return appcontrol.PIDCompletionReceipt{Binding: c.Binding, TargetProcessed: true, EffectVerified: true}, nil
		}
		select {
		case <-ctx.Done():
			return appcontrol.PIDCompletionReceipt{}, appRefusal("action_uncertain")
		case <-ticker.C:
		}
	}
}
