package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type vscreenAppInput struct {
	client *hostclient.Client
}

func (a *vscreenAppInput) Act(ctx context.Context, request vscreen.Action) (vscreen.ActionResult, error) {
	result, err := a.client.ActApp(ctx, request)
	return vscreen.ActionResult{Epoch: request.Target.Epoch, Outcome: result.Outcome}, err
}

// The containing vscreenNativeDriver owns the resource-level quiesce/dispose RPCs.
// Those native operations include app barriers and reconcile geometry exactly once.
func (a *vscreenAppInput) Quiesce(context.Context, vscreen.ResourceKey) error { return nil }
func (a *vscreenAppInput) Dispose(context.Context, vscreen.ResourceKey) error { return nil }

func (d *Daemon) startTaskVscreen(ctx context.Context, task Task, provider string, stop func(error)) (json.RawMessage, *vscreenMCP, *vscreenExecution, error) {
	key, err := d.vscreenResource(task.WorkspaceID, task.RuntimeID)
	if err != nil {
		if task.MirrorSource != nil {
			return nil, nil, nil, err
		}
		return nil, nil, nil, nil
	}
	if task.MirrorSource != nil {
		binding := protocol.MirrorChatTaskContext{Type: protocol.MirrorChatTaskContextType, Source: *task.MirrorSource}
		expectedResource := key
		expectedResource.DisplayID = task.MirrorSource.Resource.DisplayID
		if err := binding.Validate(); err != nil || task.MirrorSource.Resource != expectedResource {
			return nil, nil, nil, &vscreen.Error{Reason: protocol.VscreenSourceGone, Cause: errors.New("mirror source binding does not match task runtime")}
		}
	}
	s := d.vscreenRuntime()
	s.mu.Lock()
	defer s.mu.Unlock()
	if task.MirrorSource != nil && task.MirrorSource.Source.Kind != protocol.MirrorSourceVirtual {
		if !providerSupportsRemoteMCPBroker(provider) {
			return nil, nil, nil, errVscreenProviderUnavailable
		}
		if err = d.startVscreenHost(ctx, s); err != nil {
			return nil, nil, nil, fmt.Errorf("physical mirror source unavailable: %w", err)
		}
		sources, sourceErr := s.client.Sources(ctx, key)
		if sourceErr != nil {
			return nil, nil, nil, sourceErr
		}
		var selected *native.SourceDescriptor
		for i := range sources {
			if sources[i].MirrorSourceBinding == *task.MirrorSource && sources[i].DisplayID == task.MirrorSource.Resource.DisplayID {
				selected = &sources[i]
				break
			}
		}
		if selected == nil {
			return nil, nil, nil, &vscreen.Error{Reason: protocol.VscreenSourceGone}
		}
		injector := d.globalInjector()
		if injector == nil {
			return nil, nil, nil, &vscreen.Error{Reason: protocol.VscreenNativeUnavailable}
		}
		refresh := func(refreshCtx context.Context) (native.SourceDescriptor, error) {
			fresh, refreshErr := s.client.Sources(refreshCtx, key)
			if refreshErr != nil {
				return native.SourceDescriptor{}, refreshErr
			}
			for _, source := range fresh {
				if source.MirrorSourceBinding == *task.MirrorSource && source.DisplayID == task.MirrorSource.Resource.DisplayID {
					return source, nil
				}
			}
			return native.SourceDescriptor{}, &vscreen.Error{Reason: protocol.VscreenSourceGone}
		}
		execution := newPhysicalVscreenExecution(ctx, task, *selected, refresh, injector, d.inputArbiter, stop)
		cfg, broker, err := startVscreenMCP(ctx, execution.invoke)
		if err != nil {
			execution.Close()
			return nil, nil, nil, err
		}
		return cfg, broker, execution, nil
	}
	if !s.enabled[key] {
		if task.MirrorSource != nil {
			return nil, nil, nil, &vscreen.Error{Reason: protocol.VscreenSourceGone}
		}
		if task.VscreenContinuation != nil {
			return nil, nil, nil, &vscreen.Error{Reason: protocol.VscreenResumeUnavailable}
		}
		return nil, nil, nil, nil
	}
	if !providerSupportsRemoteMCPBroker(provider) {
		return nil, nil, nil, errVscreenProviderUnavailable
	}
	if err = d.startVscreenHost(ctx, s); err != nil {
		return nil, nil, nil, fmt.Errorf("managed virtual screen unavailable: %w", err)
	}
	actor, err := s.manager.For(key)
	if err != nil {
		return nil, nil, nil, err
	}
	if !actor.Status().Ready {
		return nil, nil, nil, errors.New("managed virtual screen is not registered")
	}
	if task.MirrorSource != nil {
		sources, err := s.client.Sources(ctx, key)
		if err != nil {
			return nil, nil, nil, err
		}
		found := false
		for _, source := range sources {
			if source.MirrorSourceBinding == *task.MirrorSource && source.DisplayID == actor.Status().Display.DisplayID {
				found = true
				break
			}
		}
		if !found {
			return nil, nil, nil, &vscreen.Error{Reason: protocol.VscreenSourceGone}
		}
		current := actor.Status().Display.Epoch
		if task.MirrorSource.NativeEpoch != current.NativeEpoch || task.MirrorSource.Generation != current.DisplayGeneration {
			return nil, nil, nil, &vscreen.Error{Reason: protocol.VscreenSourceGone, Cause: errors.New("mirror source binding is stale")}
		}
	}
	if err = d.validateVscreenContinuation(ctx, s, task, actor); err != nil {
		return nil, nil, nil, err
	}
	execution := newVscreenExecution(ctx, task, actor, s.client, func(cause error) {
		if errors.Is(cause, errVscreenIntervention) {
			if err := d.beginVscreenIntervention(task, actor, cause); err != nil {
				d.logger.Warn("virtual screen intervention persistence failed")
			}
		}
		stop(cause)
	})
	s.interventions.mu.Lock()
	if s.interventions.executions == nil {
		s.interventions.executions = map[string]*vscreenExecution{}
	}
	if s.interventions.windows == nil {
		s.interventions.windows = map[string]map[string]bool{}
	}
	s.interventions.executions[task.ID] = execution
	s.interventions.windows[task.ID] = map[string]bool{}
	s.interventions.mu.Unlock()
	execution.onClose = func() {
		s.interventions.mu.Lock()
		delete(s.interventions.executions, task.ID)
		delete(s.interventions.windows, task.ID)
		s.interventions.mu.Unlock()
	}
	// Track confirmed native ownership independently of screenshot/input proof success.
	execution.windowObserved = func(handle string) {
		s.interventions.mu.Lock()
		if handle != "" && s.interventions.executions[task.ID] == execution {
			if windows := s.interventions.windows[task.ID]; windows != nil {
				windows[handle] = true
			}
		}
		s.interventions.mu.Unlock()
	}
	cfg, broker, err := startVscreenMCP(ctx, execution.invoke)
	if err != nil {
		execution.Close()
		return nil, nil, nil, err
	}
	return cfg, broker, execution, nil
}
func mergeVscreenMCP(base, overlay json.RawMessage) (json.RawMessage, error) {
	if len(overlay) == 0 {
		return base, nil
	}
	var doc map[string]json.RawMessage
	if len(base) > 0 && string(base) != "null" {
		if err := json.Unmarshal(base, &doc); err != nil {
			return nil, err
		}
	}
	for _, key := range []string{"mcpServers", "mcp"} {
		var entries map[string]json.RawMessage
		if raw := doc[key]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &entries); err != nil {
				return nil, err
			}
		}
		if _, exists := entries[vscreenMCPName]; exists {
			return nil, errors.New("reserved MCP name multica-vscreen already configured")
		}
	}
	return mergeTaskRemoteMCPConfig(base, overlay)
}

var errVscreenProviderUnavailable = errors.New("managed virtual screen tools unavailable for this provider")

const vscreenExecutionInstructions = "\nFor GUI work use only the managed multica-vscreen tools. Do not use shell or other MCP desktop automation. Acquire a transaction, discover reusable owned handles in managed_windows, explicitly observe the selected window for fresh PNG/AX state, and never replay an uncertain action. Images returned by these tools may be sent to your configured model provider."
