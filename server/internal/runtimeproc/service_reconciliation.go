package runtimeproc

import (
	"context"
	"encoding/json"
	"time"
)

func (s *Service) reconcileCanceledMutation(root context.Context, req Request, prior Outcome) (Outcome, bool) {
	if isBuiltin(req.Operation) || s.config.ReconcileMutation == nil || root == nil || root.Err() != nil {
		return Outcome{}, false
	}
	ctx, cancel := context.WithTimeout(root, 2*time.Second)
	defer cancel()
	outcome, confirmed := s.config.ReconcileMutation(ctx, req, prior)
	if !confirmed || ctx.Err() != nil || len(outcome.Result) > maxBody || len(outcome.Result) > 0 && !json.Valid(outcome.Result) {
		return Outcome{}, false
	}
	if outcome.Error != nil && (len(outcome.Error.Code) > 128 || len(outcome.Error.Message) > 1024) {
		return Outcome{}, false
	}
	return outcome, true
}

// executionContext is selected only after durable admission; execution stays inline.
func (s *Service) executionContext(caller, root context.Context, req Request) (context.Context, context.CancelFunc) {
	if s.config.DetachMutations && (!isBuiltin(req.Operation) || req.Operation == "acknowledge") && root != nil {
		return context.WithDeadline(root, req.Deadline)
	}
	return context.WithCancel(caller)
}
