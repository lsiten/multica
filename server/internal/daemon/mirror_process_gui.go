package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (s *mirrorProcessService) acquireExecution(ctx context.Context, input mirrorProcessRequest) (json.RawMessage, error) {
	claim := input.Claim
	if claim == nil || claim.TaskID == "" || claim.AgentID == "" || claim.WorkspaceID == "" || claim.RuntimeID == "" {
		return nil, errors.New("complete GUI claim required")
	}
	if _, err := time.Parse(time.RFC3339Nano, claim.DispatchedAt); err != nil {
		return nil, errors.New("exact GUI task claim required")
	}
	if _, err := s.daemon.vscreenResource(claim.WorkspaceID, claim.RuntimeID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if len(s.executions)+len(s.acquiring) >= 128 {
		s.mu.Unlock()
		return nil, errors.New("GUI execution capacity reached")
	}
	for _, existing := range s.executions {
		if existing.claim.TaskID == claim.TaskID {
			finished := false
			if existing.released {
				select {
				case <-existing.releaseDone:
					finished = true
				default:
				}
			}
			if !finished {
				s.mu.Unlock()
				return nil, errors.New("GUI task already has an execution")
			}
		}
	}
	if s.acquiring[claim.TaskID] {
		s.mu.Unlock()
		return nil, errors.New("GUI claim already preparing")
	}
	s.acquiring[claim.TaskID] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.acquiring, claim.TaskID); s.mu.Unlock() }()
	id, err := randomBrokerToken()
	if err != nil {
		return nil, err
	}
	task := Task{ID: claim.TaskID, AgentID: claim.AgentID, WorkspaceID: claim.WorkspaceID, RuntimeID: claim.RuntimeID, DispatchedAt: claim.DispatchedAt, PriorSessionID: claim.PriorSessionID, MirrorSource: claim.Source, VscreenContinuation: claim.Continuation}
	if claim.Model != nil {
		m := claim.Model
		if len(m.APIKey) > 16384 || len(m.Endpoint) > 4096 || len(m.Model) > 512 {
			return nil, errors.New("GUI model configuration exceeds bound")
		}
		env := map[string]string{}
		switch m.Style {
		case "openai":
			env["OPENAI_BASE_URL"] = m.Endpoint
			env["OPENAI_API_KEY"] = m.APIKey
		case "anthropic":
			env["ANTHROPIC_BASE_URL"] = m.Endpoint
			env["ANTHROPIC_API_KEY"] = m.APIKey
		default:
			return nil, errors.New("unsupported GUI model style")
		}
		task.Agent = &AgentData{Model: m.Model, CustomEnv: env}
	}
	lifetime, cancel := context.WithCancel(s.bindingFor(input.Generation))
	var notifyStop sync.Once
	config, broker, execution, err := s.daemon.startTaskVscreen(context.WithValue(lifetime, mirrorGUIContextKey{}, id), task, input.Provider, func(cause error) {
		notifyStop.Do(func() {
			s.notifySafety(input.Generation, "stop_task", map[string]any{"execution_id": id, "task_id": claim.TaskID, "dispatched_at": claim.DispatchedAt, "reason": vscreenReason(cause)})
		})
	})
	if err != nil {
		cancel()
		return nil, err
	}
	if execution == nil {
		cancel()
		return marshalRaw(mirrorExecutionGrant{}), nil
	}
	if ctx.Err() != nil {
		broker.Close()
		execution.Close()
		cancel()
		return nil, ctx.Err()
	}
	s.mu.Lock()
	if s.closed || !s.bound || s.generation != input.Generation {
		s.mu.Unlock()
		broker.Close()
		execution.Close()
		cancel()
		return nil, errors.New("GUI binding changed during acquisition")
	}
	copied := *claim
	copied.Model = nil
	s.executions[id] = &mirrorChildExecution{claim: copied, execution: execution, broker: broker, config: config, cancel: cancel, releaseDone: make(chan struct{})}
	s.mu.Unlock()
	return marshalRaw(mirrorExecutionGrant{InstanceID: s.bootstrap.Identity.InstanceID, Claim: copied, ExecutionID: id, MCP: config, Active: true}), nil
}
func (s *mirrorProcessService) releaseExecution(input mirrorProcessRequest) (json.RawMessage, error) {
	s.mu.Lock()
	record, ok := s.executions[input.ExecutionID]
	if !ok {
		s.mu.Unlock()
		return nil, errors.New("GUI execution missing")
	}
	if input.Claim == nil || !mirrorClaimsEqual(record.claim, *input.Claim) {
		s.mu.Unlock()
		return nil, errors.New("GUI release claim differs")
	}
	if record.released {
		done := record.releaseDone
		s.mu.Unlock()
		<-done
		return marshalRaw(mirrorReleaseResult{InterventionPending: record.interventionPending}), nil
	}
	record.released = true
	s.mu.Unlock()
	defer close(record.releaseDone)
	record.broker.Close()
	record.cancel()
	record.execution.Close()
	s.daemon.vscreenMu.Lock()
	runtime := s.daemon.vscreen
	s.daemon.vscreenMu.Unlock()
	pending := false
	if runtime != nil {
		runtime.interventions.mu.Lock()
		intervention := runtime.interventions.records[record.claim.RuntimeID]
		pending = intervention != nil && intervention.Report.SourceTaskID == record.claim.TaskID && intervention.Report.State != protocol.VscreenInterventionContinued
		runtime.interventions.mu.Unlock()
	}
	record.execution.mu.Lock()
	record.execution.uiTars = nil
	record.execution.task.Agent = nil
	record.execution.mu.Unlock()
	s.mu.Lock()
	record.interventionPending = pending
	s.mu.Unlock()
	if !pending {
		s.mu.Lock()
		delete(s.executions, input.ExecutionID)
		s.mu.Unlock()
	}
	return marshalRaw(mirrorReleaseResult{InterventionPending: pending}), nil
}
func (s *mirrorProcessService) providerStopped(ctx context.Context, input mirrorProcessRequest) (json.RawMessage, error) {
	s.mu.Lock()
	record, ok := s.executions[input.ExecutionID]
	if !ok || input.Claim == nil || !mirrorClaimsEqual(record.claim, *input.Claim) {
		s.mu.Unlock()
		return nil, errors.New("provider stop claim differs")
	}
	s.mu.Unlock()
	if _, err := s.releaseExecution(input); err != nil {
		return nil, err
	}
	if err := s.daemon.markVscreenInterventionStopped(ctx, record.claim.TaskID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.executions, input.ExecutionID)
	s.mu.Unlock()
	return marshalRaw(struct{}{}), nil
}
func (d *Daemon) performMirrorLocal(ctx context.Context, action mirrorLocalAction) (mirrorLocalResult, error) {
	result := mirrorLocalResult{}
	if action.Action == "exclusions" {
		return result, d.updateVscreenExclusions(ctx, action.Excluded)
	}
	if _, err := d.vscreenResource(action.WorkspaceID, action.RuntimeID); err != nil {
		return result, err
	}
	switch action.Action {
	case "status":
		s := d.vscreenRuntime()
		s.interventions.mu.Lock()
		record := s.interventions.records[action.RuntimeID]
		var state protocol.VscreenInterventionState
		if record != nil {
			result.InterventionID = record.Report.InterventionID
			state = record.Report.State
			result.SelectionRequired = record.Stopped && state == protocol.VscreenInterventionAwaitingTakeover && len(record.Windows) == 0
		}
		s.interventions.mu.Unlock()
		d.vscreenMu.Lock()
		reporter := d.mirrorReportsLocked()
		d.vscreenMu.Unlock()
		if result.InterventionID != "" && (state == protocol.VscreenInterventionAwaitingTakeover || state == protocol.VscreenInterventionHuman || state == protocol.VscreenInterventionReadyToContinue) && (reporter == nil || !reporter.Acknowledged(result.InterventionID, state)) {
			return result, errors.New("report_pending")
		}
		return result, nil
	case "list_windows", "adopt_window":
		value, err := d.selectVscreenWindow(ctx, "control-attested-local", action.WorkspaceID, action.RuntimeID, action.InterventionID, action.WindowHandle, action.Action == "adopt_window")
		result.Candidates = value
		return result, err
	case "takeover":
		return result, d.TakeOverVscreenLocally(ctx, "control-attested-local", action.WorkspaceID, action.RuntimeID, action.InterventionID, action.Destination)
	case "return":
		return result, d.ReturnVscreenLocally(ctx, "control-attested-local", action.WorkspaceID, action.RuntimeID, action.InterventionID, action.Summary)
	}
	return result, &vscreen.Error{Reason: protocol.VscreenNativeUnavailable, Cause: errors.New("unsupported local mirror action")}
}

type mirrorGUIContextKey struct{}

func (d *Daemon) startVscreenExecutionMCP(ctx context.Context, execution *vscreenExecution) (json.RawMessage, *vscreenMCP, error) {
	if d.mirrorChild == nil {
		return startVscreenMCP(ctx, execution.invoke)
	}
	id, ok := ctx.Value(mirrorGUIContextKey{}).(string)
	if !ok || id == "" {
		return nil, nil, errors.New("GUI process execution identity missing")
	}
	token, err := randomBrokerToken()
	if err != nil {
		return nil, nil, err
	}
	s := d.mirrorChild
	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()
	var active atomic.Bool
	invoke := func(callCtx context.Context, name string, raw json.RawMessage) ([]map[string]any, error) {
		var args vscreenToolArgs
		acquires := name == "vscreen_acquire" && strictVscreenJSON(raw, &args) == nil && args.RequestID != "" && len(args.RequestID) <= 128 && len(args.Intent) <= 2048
		if acquires && !active.Load() {
			if _, _, err := s.events.emit(callCtx, generation, "gui_active", map[string]string{"execution_id": id}); err != nil {
				return nil, err
			}
			active.Store(true)
		}
		return execution.invoke(callCtx, name, raw)
	}
	return startVscreenMCP(ctx, invoke, token)
}
