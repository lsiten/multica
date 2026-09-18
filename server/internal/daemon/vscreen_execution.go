package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// This boundary is implemented by the authenticated native host client, never by tool input.
type vscreenAppClient interface {
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
	e := &vscreenExecution{task: task, actor: a, apps: apps, cancel: cancel, done: make(chan struct{}), lifetimeDone: lifetime.Done(), stopProvider: stop}
	go e.run(lifetime)
	return e
}
func (e *vscreenExecution) authority(l vscreen.Lease) appcontrol.Authority {
	return appcontrol.Authority{Resource: l.Resource, Epoch: e.actor.Status().Display.Epoch, TaskID: e.task.ID, TransactionID: l.TransactionID, LeaseEpoch: l.LeaseEpoch}
}
func (e *vscreenExecution) run(ctx context.Context) {
	defer close(e.done)
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
	if name == "vscreen_acquire" {
		return e.acquire(ctx, args)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	e.mu.Lock()
	defer e.mu.Unlock()
	if name == "vscreen_status" {
		s := e.actor.Status()
		return vscreenText(map[string]any{"ready": s.Ready, "frozen": s.Frozen, "waiting": s.Waiting, "owns_control": s.Lease.TaskID == e.task.ID && !s.Lease.Cancelled}), nil
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
		return e.observe(ctx, authority, window.Handle)
	case "vscreen_observe":
		return e.observe(ctx, authority, args.WindowHandle)
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
func (e *vscreenExecution) observe(ctx context.Context, a appcontrol.Authority, handle string) ([]map[string]any, error) {
	observation, err := e.apps.ObserveApp(ctx, a, handle, true)
	if err != nil {
		return nil, err
	}
	if len(observation.PNG) == 0 || observation.Width == 0 || observation.Height == 0 {
		return nil, errors.New("native snapshot unavailable")
	}
	if err = e.actor.RegisterObservation(vscreen.Observation{Lease: e.lease, Epoch: observation.Display.Epoch, WindowHandle: observation.Window.Handle, Revision: observation.Window.SnapshotRevision}); err != nil {
		return nil, err
	}
	if e.windowObserved != nil {
		e.windowObserved(observation.Window.Handle)
	}
	content := vscreenText(map[string]any{"window_handle": observation.Window.Handle, "snapshot_revision": observation.Window.SnapshotRevision, "width": observation.Width, "height": observation.Height, "bounds": observation.Window.Bounds, "elements": observation.Elements, "truncated": observation.Truncated, "pid_input_certification_configured": observation.PIDInputCertificationConfigured, "pid_input_verification": observation.PIDInputVerification, "input_policy": map[string]string{"click": "use_current_element_Press", "type": "use_current_element_SetValue", "pid_input": "requires_verified_app_os_action_certification; otherwise_human_intervention"}})
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
	return vscreenText(map[string]any{"transaction_id": e.lease.TransactionID, "lease_epoch": e.lease.LeaseEpoch}), nil
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
