package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

type mirrorProcessClient struct {
	binding    bool
	failure    error
	daemon     *Daemon
	process    *runtimeproc.Process
	identity   runtimeproc.Identity
	token      string
	endpoint   string
	http       *http.Client
	gate       chan struct{}
	mu         sync.Mutex
	generation uint64
	enqueue    func([]byte) (*wsOutbound, error)
	outbound   map[string]mirrorParentOutbound
	executions map[string]*mirrorRemoteExecution
	cancel     context.CancelFunc
	done       chan struct{}
	closeOnce  sync.Once
	closeErr   error
	uncertain  error
}
type mirrorParentOutbound struct {
	generation uint64
	outbound   *wsOutbound
}
type mirrorRemoteExecution struct {
	client       *mirrorProcessClient
	id           string
	claim        mirrorTaskClaim
	stopProvider func(error)
	once         sync.Once
	err          error
	dependent    bool
	config       json.RawMessage
	cancelWatch  func() bool
}

func (d *Daemon) mirrorProcessMode() bool { return slices.Contains(d.cfg.ProcessServices, "mirror") }

func (d *Daemon) mirrorRoster() ([]protocol.ResourceKey, error) {
	d.mu.Lock()
	pairs := [][2]string{}
	for workspace, ws := range d.workspaces {
		for _, runtimeID := range ws.runtimeIDs {
			pairs = append(pairs, [2]string{workspace, runtimeID})
		}
	}
	d.mu.Unlock()
	resources := make([]protocol.ResourceKey, 0, len(pairs))
	for _, pair := range pairs {
		resource, err := d.vscreenResource(pair[0], pair[1])
		if err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	return resources, nil
}
func (d *Daemon) ensureMirrorProcess(ctx context.Context) (*mirrorProcessClient, error) {
	d.mirrorProcessMu.Lock()
	defer d.mirrorProcessMu.Unlock()
	if d.mirrorProcessClosed {
		return nil, errors.New("mirror process closed")
	}
	if d.mirrorProcess != nil {
		return d.mirrorProcess, nil
	}
	if d.cfg.NativeVscreenPreferencesPath == "" || d.cfg.NativeHostExecutable == "" {
		return nil, errors.New("mirror process configuration missing")
	}
	resources, err := d.mirrorRoster()
	if err != nil {
		return nil, err
	}
	if len(resources) == 0 {
		return nil, errors.New("mirror has no authorized runtime")
	}
	reporter, err := d.initVscreenReporter(ctx)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(filepath.Dir(d.cfg.NativeVscreenPreferencesPath), "mirror-service")
	parent, err := filepath.EvalSymlinks(filepath.Dir(root))
	if err != nil {
		return nil, err
	}
	root = filepath.Join(parent, filepath.Base(root))
	if err = runtimeproc.PrepareRoot(root); err != nil {
		return nil, err
	}
	executable, err := filepath.EvalSymlinks(d.cfg.NativeHostExecutable)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(executable)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	f.Close()
	if err != nil {
		return nil, err
	}
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: resources[0].BackendIdentity, Account: reporter.config.AccountID, Profile: d.cfg.Profile, DaemonID: d.cfg.DaemonID, Service: "mirror"}, d.cfg.NativeHostBuild)
	if err != nil {
		return nil, err
	}
	if err = reconcileStoppedMirror(ctx, root, identity.Scope); err != nil {
		return nil, err
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		return nil, err
	}
	environment := map[string]string{}
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "WINDIR", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "MULTICA_VOICE_TRANSCRIBER", "DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "XDG_SESSION_TYPE", "DBUS_SESSION_BUS_ADDRESS", "XAUTHORITY", "LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH"} {
		if value, ok := os.LookupEnv(key); ok {
			environment[key] = value
		}
	}
	// Normal daemon shutdown drains this owned process before returning. Unknown
	// records never trigger a second owner or an in-process fallback.
	process, err := runtimeproc.Start(context.Background(), runtimeproc.LaunchConfig{Executable: executable, SHA256: hex.EncodeToString(hash.Sum(nil)), Environment: environment, Bootstrap: bootstrap, StartupTimeout: 15 * time.Second})
	if err != nil {
		return nil, err
	}
	client, err := newMirrorProcessClient(d, process, bootstrap)
	if err != nil {
		process.Close()
		return nil, err
	}
	d.mirrorProcess = client
	return client, nil
}
func newMirrorProcessClient(d *Daemon, process *runtimeproc.Process, b runtimeproc.Bootstrap) (*mirrorProcessClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := process.Client.Read(ctx, "mirror.endpoint", nil)
	if err != nil {
		return nil, err
	}
	var endpoint struct {
		Address string `json:"address"`
	}
	if json.Unmarshal(raw, &endpoint) != nil {
		return nil, errors.New("invalid mirror event endpoint")
	}
	u, err := url.Parse(endpoint.Address)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("mirror event endpoint is not loopback")
	}
	life, stop := context.WithCancel(context.Background())
	client := &mirrorProcessClient{daemon: d, process: process, identity: b.Identity, token: b.Token, endpoint: endpoint.Address, http: &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, gate: make(chan struct{}, 1), outbound: map[string]mirrorParentOutbound{}, executions: map[string]*mirrorRemoteExecution{}, cancel: stop, done: make(chan struct{})}
	go client.readEvents(life)
	return client, nil
}
func (c *mirrorProcessClient) mutation(ctx context.Context, operation string, input mirrorProcessRequest, out any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.gate <- struct{}{}:
	}
	defer func() { <-c.gate }()
	if c.uncertain != nil {
		return c.uncertain
	}
	status, err := c.process.Client.Health(ctx)
	if err != nil {
		return err
	}
	request, err := c.process.Client.Request(operation, status.Fence, marshalRaw(input))
	if err != nil {
		return err
	}
	request.Deadline = time.Now().Add(3 * time.Minute)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(request.Deadline) {
		request.Deadline = deadline
	}
	response, err := c.process.Client.Call(ctx, request)
	if err != nil {
		var rejection *runtimeproc.Error
		if errors.As(err, &rejection) {
			switch rejection.Code {
			case "unauthorized", "identity_mismatch", "stale_fence", "malformed", "unknown_operation", "retired_request", "draining", "stopped", "resource_exhausted", "deadline":
				return err
			}
		}
		c.uncertain = &mirrorUncertainOperation{RequestID: request.RequestID, Operation: operation, Cause: err}
		return c.uncertain
	}
	if response.Receipt == nil || response.Receipt.State != "completed" {
		c.uncertain = &mirrorUncertainOperation{RequestID: request.RequestID, Operation: operation, Cause: errors.New("pending operation outcome")}
		return c.uncertain
	}
	domainErr := response.Receipt.Error
	if domainErr == nil && out != nil {
		if err = json.Unmarshal(response.Receipt.Result, out); err != nil {
			c.uncertain = err
			return err
		}
	}
	ack, err := c.process.Client.Request("acknowledge", response.Status.Fence, nil)
	if err != nil {
		return err
	}
	acknowledged, err := c.process.Client.Call(ctx, ack)
	if err != nil || acknowledged.Receipt == nil || acknowledged.Receipt.State != "completed" {
		c.uncertain = &mirrorUncertainOperation{RequestID: ack.RequestID, Operation: "acknowledge", Completed: response.Receipt, Cause: errors.New("receipt acknowledgement uncertain")}
		return c.uncertain
	}
	if domainErr != nil {
		return &mirrorRemoteError{Reason: protocol.VscreenRejectionReason(domainErr.Code), Detail: domainErr.Message}
	}
	return nil
}
func (c *mirrorProcessClient) call(ctx context.Context, input mirrorProcessRequest, out any) error {
	c.mu.Lock()
	input.Generation = c.generation
	failure := c.failure
	c.mu.Unlock()
	if failure != nil {
		return failure
	}
	var accepted mirrorJobResult
	if err := c.mutation(ctx, "mirror.submit", input, &accepted); err != nil {
		var uncertain *mirrorUncertainOperation
		if errors.As(err, &uncertain) {
			jobID := uncertain.RequestID
			if uncertain.Completed != nil {
				jobID = uncertain.Completed.RequestID
			}
			if uncertain.Operation == "mirror.submit" || uncertain.Completed != nil {
				_ = c.cleanupAcceptedJob(mirrorProcessRequest{Generation: input.Generation, JobID: jobID})
			}
		}
		return err
	}
	query := mirrorProcessRequest{Generation: input.Generation, JobID: accepted.JobID}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		raw, err := c.process.Client.Read(ctx, "mirror.job", marshalRaw(query))
		if err != nil {
			return errors.Join(err, c.cleanupAcceptedJob(query))
		}
		var result mirrorJobResult
		if json.Unmarshal(raw, &result) != nil {
			return errors.New("invalid mirror job result")
		}
		if result.Done {
			if out != nil && result.Error == "" {
				if err = json.Unmarshal(result.Result, out); err != nil {
					return err
				}
			}
			if err = c.mutation(ctx, "mirror.consume_job", query, nil); err != nil {
				return err
			}
			if result.Error != "" {
				return &mirrorRemoteError{Reason: result.Reason, Detail: result.Error}
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), c.cleanupAcceptedJob(query))
		case <-tick.C:
		}
	}
}
func (c *mirrorProcessClient) bind(ctx context.Context, binding mirrorControlBinding, enqueue func([]byte) (*wsOutbound, error)) error {
	c.mu.Lock()
	old := c.generation
	c.binding = true
	c.generation = binding.Generation
	c.enqueue = enqueue
	for id, entry := range c.outbound {
		if entry.generation != binding.Generation {
			entry.outbound.cancel()
			delete(c.outbound, id)
		}
	}
	c.mu.Unlock()
	if old != 0 && old != binding.Generation {
		c.stopGUI(errors.New("mirror control connection replaced"))
	}
	err := c.mutation(ctx, "mirror.bind", mirrorProcessRequest{Binding: &binding}, nil)
	c.mu.Lock()
	c.binding = false
	c.mu.Unlock()
	return err
}
func (c *mirrorProcessClient) unbind(generation uint64) {
	c.mu.Lock()
	if c.generation != generation {
		c.mu.Unlock()
		return
	}
	c.enqueue = nil
	for id, entry := range c.outbound {
		if entry.generation == generation {
			entry.outbound.cancel()
			delete(c.outbound, id)
		}
	}
	c.mu.Unlock()
	c.stopGUI(errors.New("mirror control disconnected"))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, _ = c.bridgeRequest(ctx, "/unbind", mirrorBridgePoll{InstanceID: c.identity.InstanceID, Generation: generation}, nil)
	_ = c.mutation(ctx, "mirror.unbind", mirrorProcessRequest{Generation: generation}, nil)
}
func (c *mirrorProcessClient) bridgeRequest(ctx context.Context, path string, input any, out any) (int, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+path, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode == 204 {
		return 204, nil
	}
	if response.StatusCode != 200 {
		return response.StatusCode, errors.New("mirror event channel rejected request")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, mirrorEventBytes+1))
	if err != nil || len(body) > mirrorEventBytes {
		return response.StatusCode, errors.New("mirror event exceeds bound")
	}
	if out != nil {
		err = json.Unmarshal(body, out)
	}
	return response.StatusCode, err
}
func (c *mirrorProcessClient) readEvents(ctx context.Context) {
	defer close(c.done)
	failures := 0
	for {
		c.mu.Lock()
		generation := c.generation
		renew := c.enqueue != nil
		c.mu.Unlock()
		poll, cancel := context.WithTimeout(ctx, 5*time.Second)
		var event mirrorBridgeEvent
		status, err := c.bridgeRequest(poll, "/events", mirrorBridgePoll{InstanceID: c.identity.InstanceID, Generation: generation, Renew: renew}, &event)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			c.mu.Lock()
			transition := c.binding
			c.mu.Unlock()
			if status == 409 && transition {
				failures = 0
			} else {
				failures++
			}
			if failures >= 3 {
				c.mu.Lock()
				c.failure = errors.New("mirror child unavailable")
				c.enqueue = nil
				c.mu.Unlock()
				c.stopGUI(c.failure)
				return
			}
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		failures = 0
		if status == 204 {
			continue
		}
		eventCtx, eventCancel := context.WithDeadline(ctx, event.Deadline)
		reply := c.handleEvent(eventCtx, event)
		eventCancel()
		answer, finish := context.WithTimeout(ctx, 3*time.Second)
		_, _ = c.bridgeRequest(answer, "/reply", reply, nil)
		finish()
	}
}
func (c *mirrorProcessClient) handleEvent(ctx context.Context, event mirrorBridgeEvent) mirrorBridgeReply {
	reply := mirrorBridgeReply{InstanceID: c.identity.InstanceID, ID: event.ID, Generation: event.Generation}
	c.mu.Lock()
	generation, enqueue := c.generation, c.enqueue
	c.mu.Unlock()
	if event.InstanceID != c.identity.InstanceID || event.Generation != generation || !event.Deadline.After(time.Now()) {
		reply.Error = "stale mirror event"
		return reply
	}
	switch event.Kind {
	case "outbound":
		if err := c.validateOutbound(event.Payload); err != nil {
			reply.Error = err.Error()
			return reply
		}
		if enqueue == nil {
			reply.Error = "control writer unavailable"
			return reply
		}
		outbound, err := enqueue(event.Payload)
		if err != nil || outbound == nil {
			reply.Error = "control enqueue failed"
			return reply
		}
		c.mu.Lock()
		for id, entry := range c.outbound {
			entry.outbound.mu.Lock()
			finished := entry.outbound.sent || entry.outbound.canceled
			entry.outbound.mu.Unlock()
			if finished {
				delete(c.outbound, id)
			}
		}
		if len(c.outbound) >= 256 || c.generation != event.Generation {
			c.mu.Unlock()
			outbound.cancel()
			reply.Error = "control outbound capacity or generation changed"
			return reply
		}
		c.outbound[event.ID] = mirrorParentOutbound{generation: event.Generation, outbound: outbound}
		c.mu.Unlock()
	case "cancel_outbound":
		var input struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(event.Payload, &input) != nil {
			reply.Error = "invalid outbound cancellation"
			return reply
		}
		c.mu.Lock()
		entry, ok := c.outbound[input.ID]
		if ok && entry.generation == event.Generation {
			delete(c.outbound, input.ID)
		}
		c.mu.Unlock()
		reply.Result = marshalRaw(map[string]bool{"cancelled": ok && entry.generation == event.Generation && entry.outbound.cancel()})
	case "gui_active":
		var active struct {
			ExecutionID string `json:"execution_id"`
		}
		if json.Unmarshal(event.Payload, &active) != nil {
			reply.Error = "invalid GUI activation"
			return reply
		}
		c.mu.Lock()
		execution := c.executions[active.ExecutionID]
		if execution != nil {
			execution.dependent = true
		}
		c.mu.Unlock()
		if execution == nil {
			reply.Error = "GUI execution missing"
		}
	case "stop_task":
		var input struct {
			ExecutionID  string                          `json:"execution_id"`
			TaskID       string                          `json:"task_id"`
			DispatchedAt string                          `json:"dispatched_at"`
			Reason       protocol.VscreenRejectionReason `json:"reason"`
		}
		if json.Unmarshal(event.Payload, &input) != nil {
			reply.Error = "invalid stop request"
			return reply
		}
		c.mu.Lock()
		execution := c.executions[input.ExecutionID]
		c.mu.Unlock()
		if execution == nil || execution.claim.TaskID != input.TaskID || execution.claim.DispatchedAt != input.DispatchedAt {
			reply.Error = "stale GUI execution"
			return reply
		}
		if execution.stopProvider != nil {
			execution.stopProvider(&vscreen.Error{Reason: input.Reason, Cause: errVscreenIntervention})
		}
	case "native_fault":
		c.stopGUI(errors.New("mirror native host unavailable"))
	default:
		var input mirrorReportRequest
		if json.Unmarshal(event.Payload, &input) != nil {
			reply.Error = "invalid report request"
			return reply
		}
		d := c.daemon
		d.vscreenMu.Lock()
		reporter := d.vscreenReporter
		d.vscreenMu.Unlock()
		if reporter == nil {
			reply.Error = "report transport unavailable"
			return reply
		}
		var err error
		switch event.Kind {
		case "report_queue":
			if input.Report == nil {
				err = errors.New("report missing")
			} else {
				err = d.enqueueVscreenIntervention(ctx, *input.Report)
			}
		case "report_acknowledged":
			reply.Result = marshalRaw(map[string]bool{"acknowledged": reporter.Acknowledged(input.InterventionID, input.State)})
		case "report_consume":
			if input.Proof == nil {
				err = errors.New("proof missing")
			} else {
				err = reporter.Consume(input.WorkspaceID, input.RuntimeID, *input.Proof)
			}
		case "report_cancel":
			err = reporter.CancelScope(input.WorkspaceID, input.RuntimeID)
		case "report_epoch":
			err = reporter.InvalidateEpoch(input.WorkspaceID, input.RuntimeID, input.Epoch)
		default:
			err = errors.New("unsupported mirror event")
		}
		if err != nil {
			reply.Error = err.Error()
		}
	}
	return reply
}
func (c *mirrorProcessClient) stopGUI(cause error) {
	c.mu.Lock()
	executions := make([]*mirrorRemoteExecution, 0, len(c.executions))
	for _, execution := range c.executions {
		if execution.dependent {
			executions = append(executions, execution)
		}
	}
	c.mu.Unlock()
	for _, execution := range executions {
		if execution.stopProvider != nil {
			execution.stopProvider(&vscreen.Error{Reason: protocol.VscreenNativeUnavailable, Cause: errors.Join(errVscreenIntervention, cause)})
		}
	}
}
func (c *mirrorProcessClient) close() error {
	c.closeOnce.Do(func() {
		c.stopGUI(errors.New("mirror process stopping"))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		c.closeErr = c.process.Stop(ctx)
		if c.closeErr != nil {
			c.closeErr = errors.Join(c.closeErr, c.process.Close())
		}
		c.cancel()
		<-c.done
	})
	return c.closeErr
}

type mirrorUncertainOperation struct {
	RequestID, Operation string
	Completed            *runtimeproc.Receipt
	Cause                error
}

func (e *mirrorUncertainOperation) Error() string {
	return fmt.Sprintf("mirror %s request %s requires reconciliation: %v", e.Operation, e.RequestID, e.Cause)
}
func (e *mirrorUncertainOperation) Unwrap() error { return e.Cause }

// Cleanup is an exact-job revocation, never a replay or a blanket receipt ACK.
func (c *mirrorProcessClient) cleanupAcceptedJob(input mirrorProcessRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.gate <- struct{}{}:
	}
	defer func() { <-c.gate }()
	status, err := c.process.Client.Health(ctx)
	if err != nil {
		return err
	}
	request, err := c.process.Client.Request("mirror.cancel_job", status.Fence, marshalRaw(input))
	if err != nil {
		return err
	}
	response, err := c.process.Client.Call(ctx, request)
	if err != nil {
		return err
	}
	if response.Receipt == nil || response.Receipt.State != "completed" {
		return errors.New("mirror cleanup job unconfirmed")
	}
	return nil
}

func (c *mirrorProcessClient) validateOutbound(raw json.RawMessage) error {
	var frame protocol.Message
	if json.Unmarshal(raw, &frame) != nil {
		return errors.New("invalid mirror outbound frame")
	}
	switch frame.Type {
	case protocol.EventMirrorAnswer, protocol.EventMirrorAnswerFailure, protocol.EventMirrorViewer, protocol.EventMirrorControlState, protocol.EventVscreenQueryResult, protocol.EventVscreenResult:
	default:
		return errors.New("mirror outbound event is not authorized")
	}
	var scope struct {
		WorkspaceID string `json:"workspace_id"`
		RuntimeID   string `json:"runtime_id"`
		DaemonID    string `json:"daemon_id"`
	}
	if json.Unmarshal(frame.Payload, &scope) != nil || scope.WorkspaceID == "" || scope.RuntimeID == "" {
		return errors.New("mirror outbound scope missing")
	}
	if c.daemon == nil {
		return errors.New("mirror outbound control owner missing")
	}
	if scope.DaemonID != "" && scope.DaemonID != c.daemon.cfg.DaemonID {
		return errors.New("mirror outbound daemon differs")
	}
	_, err := c.daemon.vscreenResource(scope.WorkspaceID, scope.RuntimeID)
	return err
}

type mirrorRemoteError struct {
	Reason protocol.VscreenRejectionReason
	Detail string
}

func (e *mirrorRemoteError) Error() string { return e.Detail }
func (e *mirrorRemoteError) Unwrap() error {
	var cause error
	if e.Detail == context.Canceled.Error() {
		cause = context.Canceled
	}
	if e.Detail == context.DeadlineExceeded.Error() {
		cause = context.DeadlineExceeded
	}
	return errors.Join(&vscreen.Error{Reason: e.Reason}, cause)
}
