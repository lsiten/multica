package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/multica-ai/multica/server/internal/computeruse"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/internal/vscreen/native/globalinput"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// This boundary is implemented by the authenticated native host client, never by tool input.
type vscreenAppClient interface {
	ManagedAppWindows(context.Context, appcontrol.Authority) ([]appcontrol.ManagedWindow, error)
	ListApps(context.Context, appcontrol.Authority) (appcontrol.AppList, error)
	Grant(context.Context, appcontrol.Authority, time.Duration) error
	Renew(context.Context, appcontrol.Authority, time.Duration) error
	Revoke(context.Context, appcontrol.Authority) error
	ResumeApps(context.Context, appcontrol.Authority) error
	LaunchApp(context.Context, appcontrol.Authority, appcontrol.LaunchRequest) (appcontrol.Window, error)
	ObserveApp(context.Context, appcontrol.Authority, string, bool) (appcontrol.Observation, error)
}
type vscreenTicket struct {
	done  chan struct{}
	lease vscreen.Lease
	err   error
}
type vscreenExecution struct {
	nativeGranted  atomic.Bool
	mu             sync.Mutex
	task           Task
	actor          *vscreen.Actor
	apps           vscreenAppClient
	lease          vscreen.Lease
	ticket         *vscreenTicket
	requestID      string
	cancel         context.CancelFunc
	done           chan struct{}
	lifetimeDone   <-chan struct{}
	onClose        func()
	windowObserved func(string)
	stopProvider   func(error)
	physical       *physicalVscreenTarget
	uiTars         *computeruse.UITARS
}

// physicalVscreenTarget is the deliberately narrow agent path for an
// explicitly selected physical/system source. It has no app/PID authority;
// every action is posted through the same global injector used by human
// mirror control and is serialized by the daemon arbiter.
type physicalVscreenTarget struct {
	descriptor native.SourceDescriptor
	refresh    func(context.Context) (native.SourceDescriptor, error)
	injector   globalinput.Injector
	arbiter    *mirror.Arbiter
	mu         sync.Mutex
	lease      vscreen.Lease
}

var errVscreenStopUnconfirmed = errors.New("virtual screen provider stop unconfirmed")
var errVscreenIntervention = errors.New("gui_human_intervention")
var errVscreenToolArguments = errors.New("invalid virtual screen arguments")

func safeVscreenToolError(err error) string {
	if errors.Is(err, errVscreenToolArguments) {
		return errVscreenToolArguments.Error()
	}
	if errors.Is(err, errVscreenIntervention) {
		return errVscreenIntervention.Error()
	}
	return string(vscreenReason(err))
}
func newVscreenExecution(ctx context.Context, task Task, a *vscreen.Actor, apps vscreenAppClient, stop func(error)) *vscreenExecution {
	lifetime, cancel := context.WithCancel(ctx)
	e := &vscreenExecution{task: task, actor: a, apps: apps, cancel: cancel, done: make(chan struct{}), lifetimeDone: lifetime.Done(), stopProvider: stop, uiTars: configuredUITARS(task)}
	go e.run(lifetime)
	return e
}

func newPhysicalVscreenExecution(ctx context.Context, task Task, descriptor native.SourceDescriptor, refresh func(context.Context) (native.SourceDescriptor, error), injector globalinput.Injector, arbiter *mirror.Arbiter, stop func(error)) *vscreenExecution {
	lifetime, cancel := context.WithCancel(ctx)
	e := &vscreenExecution{
		task: task, cancel: cancel, done: make(chan struct{}), lifetimeDone: lifetime.Done(), stopProvider: stop,
		physical: &physicalVscreenTarget{descriptor: descriptor, refresh: refresh, injector: injector, arbiter: arbiter},
		uiTars:   configuredUITARS(task),
	}
	go e.run(lifetime)
	return e
}

func configuredUITARS(task Task) *computeruse.UITARS {
	if task.Agent == nil {
		return nil
	}
	env := task.Agent.CustomEnv
	endpoint := env["OPENAI_BASE_URL"]
	if endpoint == "" {
		endpoint = env["OPENAI_API_BASE"]
	}
	style := "openai"
	apiKey := env["OPENAI_API_KEY"]
	if endpoint == "" && env["ANTHROPIC_API_KEY"] != "" {
		endpoint = env["ANTHROPIC_BASE_URL"]
		if endpoint == "" {
			endpoint = "https://api.anthropic.com/v1/messages"
		}
		style = "anthropic"
		apiKey = env["ANTHROPIC_API_KEY"]
	}
	if endpoint == "" {
		return nil
	}
	if style == "openai" && !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint = strings.TrimRight(endpoint, "/") + "/chat/completions"
	}
	model := task.Agent.Model
	if model == "" {
		return nil
	}
	client, err := computeruse.NewUITARS(computeruse.UITARSConfig{Endpoint: endpoint, Model: model, APIKey: apiKey, Style: style})
	if err != nil {
		return nil
	}
	return client
}
func (e *vscreenExecution) authority(l vscreen.Lease) appcontrol.Authority {
	return appcontrol.Authority{Resource: l.Resource, Epoch: e.actor.Status().Display.Epoch, TaskID: e.task.ID, TransactionID: l.TransactionID, LeaseEpoch: l.LeaseEpoch}
}
func (e *vscreenExecution) run(ctx context.Context) {
	defer close(e.done)
	if e.physical != nil {
		<-ctx.Done()
		e.physical.mu.Lock()
		if e.physical.lease.TransactionID != "" && e.physical.arbiter != nil {
			e.physical.arbiter.Release(e.physical.descriptor.Resource, mirror.Principal{Kind: mirror.PrincipalAgent, ID: e.task.ID}, e.physical.lease.TransactionID)
			e.physical.lease = vscreen.Lease{}
		}
		e.physical.mu.Unlock()
		_ = e.physical.injector.Close()
		return
	}
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			e.mu.Lock()
			ticket := e.ticket
			e.mu.Unlock()
			if ticket != nil {
				<-ticket.done
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if e.lease.TransactionID == "" && ticket != nil && ticket.err == nil {
				e.lease = ticket.lease
			}
			if e.lease.TransactionID != "" {
				_ = e.apps.Revoke(cleanup, e.authority(e.lease))
				_ = e.actor.Release(cleanup, e.lease)
			}
			return
		case <-ticker.C:
			lease := e.actor.Status().Lease
			if e.nativeGranted.Load() && lease.TaskID == e.task.ID && !lease.Cancelled && lease.TransactionID != "" {
				renewed, err := e.actor.Heartbeat(lease)
				if err == nil {
					err = e.apps.Renew(ctx, e.authority(renewed), time.Until(renewed.ExpiresAt))
				}
				if err != nil && e.nativeGranted.Load() && ctx.Err() == nil {
					e.freeze(err)
				}
			}
		}
	}
}
func (e *vscreenExecution) Close() {
	e.cancel()
	<-e.done
	if e.onClose != nil {
		e.onClose()
	}
}
func (e *vscreenExecution) freeze(cause error) {
	e.nativeGranted.Store(false)
	cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// The actor freezes synchronously before waiting for the native quiescence barrier.
	_ = e.actor.Suspend(cleanup)
	lease := e.actor.Status().Lease
	if lease.TaskID == e.task.ID && lease.TransactionID != "" {
		_ = e.apps.Revoke(cleanup, e.authority(lease))
	}
	if e.stopProvider != nil {
		e.stopProvider(&vscreen.Error{Reason: vscreenReason(cause), Cause: errors.Join(errVscreenIntervention, cause)})
	}
}

type vscreenToolArgs struct {
	RequestID        string                  `json:"request_id,omitempty"`
	Intent           string                  `json:"intent,omitempty"`
	TransactionID    string                  `json:"transaction_id,omitempty"`
	BundleID         string                  `json:"bundle_id,omitempty"`
	Files            []string                `json:"files,omitempty"`
	WindowHandle     string                  `json:"window_handle,omitempty"`
	SnapshotRevision uint64                  `json:"snapshot_revision,omitempty"`
	ActionID         string                  `json:"action_id,omitempty"`
	Sequence         uint64                  `json:"sequence,omitempty"`
	Goal             string                  `json:"goal,omitempty"`
	Action           *protocol.VscreenAction `json:"action,omitempty"`
}

func vscreenText(v any) []map[string]any {
	raw, _ := json.Marshal(v)
	return []map[string]any{{"type": "text", "text": string(raw)}}
}
func (e *vscreenExecution) invoke(ctx context.Context, name string, raw json.RawMessage) ([]map[string]any, error) {
	var args vscreenToolArgs
	if strictVscreenJSON(raw, &args) != nil {
		return nil, errVscreenToolArguments
	}
	select {
	case <-e.lifetimeDone:
		return nil, context.Canceled
	default:
	}
	if e.physical != nil {
		if name == "vscreen_ui_tars" {
			return e.invokeUITARS(ctx, args)
		}
		return e.invokePhysical(ctx, name, args)
	}
	if name == "vscreen_ui_tars" {
		return e.invokeUITARS(ctx, args)
	}
	if binding := e.task.MirrorSource; binding != nil {
		current := e.actor.Status()
		if !current.Ready || current.Display.Resource != binding.Resource || current.Display.Epoch.NativeEpoch != binding.NativeEpoch || current.Display.Epoch.DisplayGeneration != binding.Generation {
			return nil, &vscreen.Error{Reason: protocol.VscreenSourceGone}
		}
	}
	if name == "vscreen_acquire" {
		return e.acquire(ctx, args)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	e.mu.Lock()
	defer e.mu.Unlock()
	if name == "vscreen_status" {
		s := e.actor.Status()
		owns := s.Lease.TaskID == e.task.ID && !s.Lease.Cancelled && e.nativeGranted.Load() && e.lease.TransactionID != "" && s.Lease.TransactionID == e.lease.TransactionID && s.Lease.LeaseEpoch == e.lease.LeaseEpoch
		windows := []appcontrol.ManagedWindow{}
		if owns {
			var err error
			windows, err = e.apps.ManagedAppWindows(ctx, e.authority(e.lease))
			if err != nil {
				return nil, err
			}
		}
		return vscreenText(map[string]any{"ready": s.Ready, "frozen": s.Frozen, "waiting": s.Waiting, "owns_control": owns, "managed_windows": windows}), nil
	}
	if args.TransactionID == "" || args.TransactionID != e.lease.TransactionID {
		return nil, errVscreenToolArguments
	}
	authority := e.authority(e.lease)
	switch name {
	case "vscreen_release":
		e.nativeGranted.Store(false)
		if err := e.apps.Revoke(ctx, authority); err != nil {
			e.freeze(err)
			return nil, err
		}
		if err := e.actor.Release(ctx, e.lease); err != nil {
			e.freeze(err)
			return nil, err
		}
		e.lease = vscreen.Lease{}
		e.ticket = nil
		e.requestID = ""
		return vscreenText(map[string]any{"released": true}), nil
	case "vscreen_list_apps":
		apps, err := e.apps.ListApps(ctx, authority)
		if err != nil {
			return nil, err
		}
		return vscreenText(apps), nil
	case "vscreen_launch_app":
		window, err := e.apps.LaunchApp(ctx, authority, appcontrol.LaunchRequest{BundleID: args.BundleID, Files: args.Files})
		if err != nil {
			e.freeze(err)
			return nil, err
		}
		if window.Process.BundleID != args.BundleID || appcontrol.ValidateManagedWindows([]appcontrol.ManagedWindow{{Handle: window.Handle, BundleID: window.Process.BundleID}}) != nil {
			e.freeze(errVscreenToolArguments)
			return nil, errVscreenToolArguments
		}
		if e.windowObserved != nil {
			e.windowObserved(window.Handle)
		}
		return e.observe(ctx, authority, window.Handle, true)
	case "vscreen_observe":
		return e.observe(ctx, authority, args.WindowHandle, false)
	case "vscreen_click", "vscreen_drag", "vscreen_scroll", "vscreen_type", "vscreen_key":
		if args.Action == nil || "vscreen_"+string(args.Action.Kind) != name {
			return nil, errVscreenToolArguments
		}
		request := protocol.VscreenActionRequest{Target: protocol.VscreenActionTarget{Resource: authority.Resource, Epoch: authority.Epoch, TaskID: e.task.ID, TransactionID: authority.TransactionID, LeaseEpoch: authority.LeaseEpoch, WindowHandle: args.WindowHandle, SnapshotRevision: args.SnapshotRevision}, ActionID: args.ActionID, Sequence: args.Sequence, Action: *args.Action}
		if err := request.Validate(); err != nil {
			return nil, errVscreenToolArguments
		}
		result, err := e.actor.Execute(ctx, request)
		if e.actor.Status().Frozen {
			e.freeze(err)
			return nil, errVscreenIntervention
		}
		if err != nil {
			return nil, err
		}
		return vscreenText(result), nil
	default:
		return nil, errVscreenToolArguments
	}
}

func (e *vscreenExecution) invokePhysical(ctx context.Context, name string, args vscreenToolArgs) ([]map[string]any, error) {
	p := e.physical
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refresh != nil {
		current, err := p.refresh(ctx)
		if err != nil || current.MirrorSourceBinding != p.descriptor.MirrorSourceBinding || current.DisplayID != p.descriptor.DisplayID || current.GeometryRevision != p.descriptor.GeometryRevision {
			return nil, &vscreen.Error{Reason: protocol.VscreenSourceGone}
		}
		p.descriptor = current
	}
	snapshot := func() ([]map[string]any, error) {
		d := p.descriptor
		bounds := image.Rect(int(d.X), int(d.Y), int(d.X)+int(d.LogicalWidth), int(d.Y)+int(d.LogicalHeight))
		frame, err := (mirror.NativeCapturer{NoPermissionPrompt: true, Bounds: &bounds}).Capture(ctx)
		if err != nil {
			return nil, err
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, frame); err != nil {
			return nil, err
		}
		meta := map[string]any{"observation_scope": "display", "window_handle": "display-focus", "snapshot_revision": d.GeometryRevision, "width": frame.Bounds().Dx(), "height": frame.Bounds().Dy(), "elements": []any{}, "physical_source": true, "input_policy": map[string]string{"click": "coordinate", "type": "focused_display", "pid_input": "unavailable"}}
		return append(vscreenText(meta), map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(encoded.Bytes())}), nil
	}
	switch name {
	case "vscreen_status":
		return vscreenText(map[string]any{"ready": true, "frozen": false, "owns_control": p.lease.TransactionID != "", "physical_source": true}), nil
	case "vscreen_observe":
		return snapshot()
	case "vscreen_acquire":
		if args.RequestID == "" || len(args.RequestID) > 128 || p.lease.TransactionID != "" {
			return nil, errVscreenToolArguments
		}
		id, err := randomBrokerToken()
		if err != nil {
			return nil, err
		}
		p.lease = vscreen.Lease{Resource: p.descriptor.Resource, TransactionID: id, LeaseEpoch: 1, TaskID: e.task.ID, ExpiresAt: time.Now().Add(agentGestureTTL)}
		return vscreenText(map[string]any{"transaction_id": id, "lease_epoch": 1, "managed_windows": []any{}, "physical_source": true}), nil
	case "vscreen_release":
		if p.lease.TransactionID == "" || args.TransactionID != p.lease.TransactionID {
			return nil, errVscreenToolArguments
		}
		if p.arbiter != nil {
			p.arbiter.Release(p.descriptor.Resource, mirror.Principal{Kind: mirror.PrincipalAgent, ID: e.task.ID}, p.lease.TransactionID)
		}
		p.lease = vscreen.Lease{}
		return vscreenText(map[string]any{"released": true}), nil
	}
	if p.lease.TransactionID == "" || args.TransactionID != p.lease.TransactionID || args.Action == nil || args.Action.Validate() != nil {
		return nil, errVscreenToolArguments
	}
	if !p.injector.Available() {
		return nil, &vscreen.Error{Reason: protocol.VscreenPermissionDenied}
	}
	gesture := args.ActionID
	if gesture == "" {
		gesture = p.lease.TransactionID
	}
	principal := mirror.Principal{Kind: mirror.PrincipalAgent, ID: e.task.ID}
	if p.arbiter != nil {
		if p.arbiter.Acquire(p.descriptor.Resource, principal, gesture, agentGestureTTL) == mirror.AcquireBusy {
			return nil, &vscreen.Error{Reason: protocol.VscreenAppInUse}
		}
		defer p.arbiter.Release(p.descriptor.Resource, principal, gesture)
	}
	d := p.descriptor
	toDisplay := func(point protocol.VscreenPoint) (float64, float64) {
		w, h := float64(d.Width), float64(d.Height)
		if w <= 0 || h <= 0 {
			w, h = d.LogicalWidth, d.LogicalHeight
		}
		return point.X/w*d.LogicalWidth + float64(d.X), point.Y/h*d.LogicalHeight + float64(d.Y)
	}
	var err error
	switch args.Action.Kind {
	case protocol.VscreenActionClick:
		if args.Action.Click.Position == nil {
			return nil, errVscreenToolArguments
		}
		x, y := toDisplay(*args.Action.Click.Position)
		err = p.injector.Pointer(globalinput.PointerEvent{Kind: protocol.MirrorInputPointerDown, Button: globalinput.ButtonLeft, X: x, Y: y})
		if err == nil {
			err = p.injector.Pointer(globalinput.PointerEvent{Kind: protocol.MirrorInputPointerUp, Button: globalinput.ButtonLeft, X: x, Y: y})
		}
	case protocol.VscreenActionDrag:
		fromX, fromY := toDisplay(args.Action.Drag.From)
		toX, toY := toDisplay(args.Action.Drag.To)
		err = p.injector.Pointer(globalinput.PointerEvent{Kind: protocol.MirrorInputPointerDown, Button: globalinput.ButtonLeft, X: fromX, Y: fromY})
		if err == nil {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(args.Action.Drag.DurationMS) * time.Millisecond):
			}
		}
		if err == nil {
			err = p.injector.Pointer(globalinput.PointerEvent{Kind: protocol.MirrorInputPointerMove, Button: globalinput.ButtonLeft, X: toX, Y: toY})
		}
		if err == nil {
			err = p.injector.Pointer(globalinput.PointerEvent{Kind: protocol.MirrorInputPointerUp, Button: globalinput.ButtonLeft, X: toX, Y: toY})
		}
	case protocol.VscreenActionScroll:
		x, y := toDisplay(args.Action.Scroll.Position)
		err = p.injector.Pointer(globalinput.PointerEvent{Kind: protocol.MirrorInputWheel, X: x, Y: y, DeltaX: args.Action.Scroll.DeltaX, DeltaY: args.Action.Scroll.DeltaY})
	case protocol.VscreenActionType:
		err = p.injector.Text(args.Action.Type.Text)
	case protocol.VscreenActionKey:
		err = p.injector.Key(globalinput.KeyEvent{Key: args.Action.Key.Key, Modifiers: args.Action.Key.Modifiers, Down: true})
		if err == nil {
			err = p.injector.Key(globalinput.KeyEvent{Key: args.Action.Key.Key, Modifiers: args.Action.Key.Modifiers, Down: false})
		}
	}
	if err != nil {
		return nil, err
	}
	return vscreenText(map[string]any{"outcome": "dispatched", "physical_source": true}), nil
}
func (e *vscreenExecution) observe(ctx context.Context, a appcontrol.Authority, handle string, ownershipConfirmed bool) ([]map[string]any, error) {
	if handle != "" && !ownershipConfirmed {
		windows, err := e.apps.ManagedAppWindows(ctx, a)
		if err != nil {
			return nil, err
		}
		found := false
		for _, w := range windows {
			if w.Handle == handle {
				found = true
				break
			}
		}
		if !found {
			return nil, &vscreen.Error{Reason: protocol.VscreenStaleSnapshot}
		}
		if e.windowObserved != nil {
			e.windowObserved(handle)
		}
	}
	observation, err := e.apps.ObserveApp(ctx, a, handle, true)
	if err != nil {
		return nil, err
	}
	if len(observation.PNG) == 0 || observation.Width == 0 || observation.Height == 0 {
		return nil, errors.New("native snapshot unavailable")
	}
	if observation.Window.Handle != handle || observation.Display.Epoch != a.Epoch {
		return nil, &vscreen.Error{Reason: protocol.VscreenStaleSnapshot}
	}
	if handle == "" {
		windows, err := e.apps.ManagedAppWindows(ctx, a)
		if err != nil {
			return nil, err
		}
		content := vscreenText(map[string]any{"observation_scope": "display", "window_handle": "", "snapshot_revision": 0, "width": observation.Width, "height": observation.Height, "managed_windows": windows})
		return append(content, map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(observation.PNG)}), nil
	}
	if err = e.actor.RegisterObservation(vscreen.Observation{Lease: e.lease, Epoch: observation.Display.Epoch, WindowHandle: handle, Revision: observation.Window.SnapshotRevision}); err != nil {
		return nil, err
	}
	content := vscreenText(map[string]any{"window_handle": observation.Window.Handle, "snapshot_revision": observation.Window.SnapshotRevision, "width": observation.Width, "height": observation.Height, "bounds": observation.Window.Bounds, "elements": observation.Elements, "truncated": observation.Truncated, "pid_input_certification_configured": observation.PIDInputCertificationConfigured, "pid_input_verification": observation.PIDInputVerification, "pid_input_completion_available": observation.PIDInputCompletionAvailable, "input_policy": map[string]string{"click": "use_current_element_Press", "type": "use_current_element_SetValue", "pid_input": "requires_verified_app_os_action_certification; otherwise_human_intervention"}})
	return append(content, map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(observation.PNG)}), nil
}
func (e *vscreenExecution) acquire(ctx context.Context, args vscreenToolArgs) ([]map[string]any, error) {
	if args.RequestID == "" || len(args.RequestID) > 128 || len(args.Intent) > 2048 {
		return nil, errVscreenToolArguments
	}
	e.mu.Lock()
	if e.ticket != nil && e.requestID != args.RequestID {
		e.mu.Unlock()
		return nil, errVscreenToolArguments
	}
	if e.ticket == nil {
		txID, err := randomBrokerToken()
		if err != nil {
			e.mu.Unlock()
			return nil, err
		}
		ticket := &vscreenTicket{done: make(chan struct{})}
		e.ticket = ticket
		e.requestID = args.RequestID
		// Retain one FIFO waiter across bounded MCP polls; only task cancellation removes it.
		go func() {
			lifetime, cancel := context.WithCancel(context.Background())
			defer cancel()

			go func() {
				select {
				case <-e.lifetimeDone:
					cancel()
				case <-lifetime.Done():
				}
			}()
			ticket.lease, ticket.err = e.actor.Acquire(lifetime, vscreen.Transaction{TaskID: e.task.ID, ID: txID})
			close(ticket.done)
		}()
	}
	ticket := e.ticket
	e.mu.Unlock()
	select {
	case <-ctx.Done():
		return vscreenText(map[string]any{"waiting_ticket": args.RequestID}), nil
	case <-ticket.done:
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ticket.err != nil {
		return nil, ticket.err
	}
	if e.lease.TransactionID == "" {
		e.lease = ticket.lease
		a := e.authority(e.lease)
		if err := e.apps.Grant(ctx, a, time.Until(e.lease.ExpiresAt)); err != nil {
			e.freeze(err)
			return nil, err
		}
		if err := e.apps.ResumeApps(ctx, a); err != nil {
			e.freeze(err)
			return nil, err
		}
		e.nativeGranted.Store(true)
	}
	windows, err := e.apps.ManagedAppWindows(ctx, e.authority(e.lease))
	if err != nil {
		return nil, err
	}
	return vscreenText(map[string]any{"transaction_id": e.lease.TransactionID, "lease_epoch": e.lease.LeaseEpoch, "managed_windows": windows}), nil
}

func finalizeVscreenStop(parent, execution context.Context, result *TaskResult, runErr *error) {
	if parent.Err() != nil || !errors.Is(context.Cause(execution), errVscreenIntervention) || result.Status == "completed" || errors.Is(*runErr, errVscreenStopUnconfirmed) {
		return
	}
	result.Status = "blocked"
	result.FailureReason = protocol.VscreenPauseReasonHumanIntervention
	result.Comment = "Stopped for human intervention in the managed virtual screen."
	*runErr = nil
}
