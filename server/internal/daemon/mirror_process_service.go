package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

type mirrorProcessService struct {
	daemon           *Daemon
	bootstrap        runtimeproc.Bootstrap
	events           *mirrorEventBridge
	mu               sync.Mutex
	generation       uint64
	serverGeneration string
	bound            bool
	bindingDeadline  time.Time
	bindingContext   context.Context
	cancelBinding    context.CancelFunc
	jobs             map[string]*mirrorProcessJob
	executions       map[string]*mirrorChildExecution
	workers          sync.WaitGroup
	stop             chan struct{}
	stopped          chan struct{}
	closed           bool
	shutdownErr      error
	nativeWorkers    sync.WaitGroup
	notifications    sync.WaitGroup
	acquiring        map[string]bool
}
type mirrorProcessJob struct {
	result     mirrorJobResult
	generation uint64
	cancel     context.CancelFunc
	expires    time.Time
	input      mirrorProcessRequest
}
type mirrorChildExecution struct {
	claim               mirrorTaskClaim
	execution           *vscreenExecution
	broker              *vscreenMCP
	config              json.RawMessage
	cancel              context.CancelFunc
	released            bool
	releaseDone         chan struct{}
	interventionPending bool
}

// RunMirrorService runs only mirror/native authority, never daemon control loops.
func RunMirrorService(ctx context.Context, b runtimeproc.Bootstrap) error {
	if b.Identity.Scope.Service != "mirror" {
		return errors.New("unsupported mirror role")
	}
	if err := runtimeproc.PrepareRoot(b.Root); err != nil {
		return err
	}
	domainLock, err := lockMirrorProcessDomain(filepath.Join(b.Root, "authority.lock"))
	if err != nil {
		return err
	}
	defer domainLock.Close()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	d := &Daemon{cfg: Config{ServerBaseURL: b.Identity.Scope.Backend, DaemonID: b.Identity.Scope.DaemonID, Profile: b.Identity.Scope.Profile, NativeHostExecutable: executable, NativeHostBuild: b.Identity.Build, NativeVscreenPreferencesPath: filepath.Join(filepath.Dir(b.Root), "vscreen-enabled.json")}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workspaces: map[string]*workspaceState{}, runtimeIndex: map[string]Runtime{}, runtimeMirrors: map[string]*mirror.RuntimeMirror{}, inputArbiter: mirror.NewArbiter()}
	s := &mirrorProcessService{daemon: d, bootstrap: b, acquiring: map[string]bool{}, jobs: map[string]*mirrorProcessJob{}, executions: map[string]*mirrorChildExecution{}, stop: make(chan struct{}), stopped: make(chan struct{})}
	d.mirrorChild = s
	d.mirrorReportBridge = &mirrorChildReports{service: s}
	d.SetVscreenLocalOwnerVerifier(func(_ context.Context, capability string) bool { return capability == "control-attested-local" })
	process, err := runtimeproc.NewService(runtimeproc.Config{Bootstrap: b, Capabilities: []string{"mirror.bind", "mirror.unbind", "mirror.submit", "mirror.cancel_job", "mirror.consume_job"}, ReadCapabilities: []string{"mirror.endpoint", "mirror.job", "mirror.inventory"}, Handler: s.mutate, ReadHandler: s.read, Ready: s.ready, Shutdown: s.shutdown})
	if err != nil {
		return err
	}
	return process.Serve(ctx)
}
func (s *mirrorProcessService) ready(ctx context.Context) error {
	bridge, err := newMirrorEventBridge(s.bootstrap.Token, s.bootstrap.Identity.InstanceID, s.poll)
	if err != nil {
		close(s.stopped)
		return err
	}
	s.events = bridge
	s.bindingContext = ctx
	go func() {
		defer close(s.stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				expired := s.bound && time.Now().After(s.bindingDeadline)
				generation := s.generation
				expiredJobs := []*mirrorProcessJob{}
				for id, job := range s.jobs {
					if time.Now().After(job.expires) {
						job.cancel()
						if job.result.Done {
							delete(s.jobs, id)
							expiredJobs = append(expiredJobs, job)
						}
					}
				}
				s.mu.Unlock()
				for _, job := range expiredJobs {
					_ = s.cleanupJobResult(job.input, job.result.Result)
				}
				if expired {
					s.unbind(generation)
				}
			}
		}
	}()
	return nil
}
func (s *mirrorProcessService) poll(generation uint64, renew bool) bool {
	s.mu.Lock()
	if s.closed || generation != s.generation {
		s.mu.Unlock()
		return false
	}
	bound := s.bound
	if renew && bound {
		s.bindingDeadline = time.Now().Add(8 * time.Second)
	}
	s.mu.Unlock()
	if !renew && bound {
		s.unbind(generation)
	}
	return true
}
func (s *mirrorProcessService) read(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	if request.Operation == "mirror.inventory" {
		return s.inventory(), nil
	}
	if request.Operation == "mirror.endpoint" {
		return marshalRaw(map[string]string{"address": s.events.address}), nil
	}
	var input mirrorProcessRequest
	if json.Unmarshal(request.Payload, &input) != nil {
		return nil, mirrorIPCError(errors.New("malformed mirror query"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[input.JobID]
	if !ok || job.generation != input.Generation {
		return nil, mirrorIPCError(errors.New("mirror job not found for generation"))
	}
	return marshalRaw(job.result), nil
}
func (s *mirrorProcessService) mutate(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	var input mirrorProcessRequest
	if json.Unmarshal(request.Payload, &input) != nil {
		return nil, mirrorIPCError(errors.New("malformed mirror request"))
	}
	switch request.Operation {
	case "mirror.bind":
		if input.Binding == nil {
			return nil, mirrorIPCError(errors.New("mirror binding required"))
		}
		return marshalRaw(struct{}{}), mirrorIPCError(s.bind(*input.Binding))
	case "mirror.unbind":
		s.unbind(input.Generation)
		return marshalRaw(struct{}{}), nil
	case "mirror.cancel_job", "mirror.consume_job":
		s.mu.Lock()
		job, ok := s.jobs[input.JobID]
		if !ok || job.generation != input.Generation {
			s.mu.Unlock()
			return nil, mirrorIPCError(errors.New("mirror job missing"))
		}
		if request.Operation == "mirror.consume_job" {
			if !job.result.Done {
				s.mu.Unlock()
				return nil, mirrorIPCError(errors.New("mirror job pending"))
			}
			delete(s.jobs, input.JobID)
			s.mu.Unlock()
			return marshalRaw(struct{}{}), nil
		}
		job.cancel()
		finished := job.result.Done
		result := job.result.Result
		original := job.input
		s.mu.Unlock()
		if finished {
			if err := s.cleanupJobResult(original, result); err != nil {
				return nil, mirrorIPCError(err)
			}
		}
		return marshalRaw(struct{}{}), nil
	case "mirror.submit":
		return s.submit(request, input)
	}
	return nil, mirrorIPCError(errors.New("unknown mirror operation"))
}
func (s *mirrorProcessService) bind(binding mirrorControlBinding) error {
	if binding.Generation == 0 || binding.ServerGeneration == "" || len(binding.Resources) > 256 {
		return errors.New("invalid control binding")
	}
	seen := map[string]bool{}
	for _, resource := range binding.Resources {
		if resource.Validate() != nil || resource.DisplayID != 0 || resource.BackendIdentity != s.bootstrap.Identity.Scope.Backend || resource.UID != uint32(os.Getuid()) || seen[resource.RuntimeID] {
			return errors.New("invalid authorized mirror roster")
		}
		seen[resource.RuntimeID] = true
	}
	s.mu.Lock()
	old := s.generation
	if s.closed || binding.Generation < old || binding.Generation == old && s.serverGeneration != "" && binding.ServerGeneration != s.serverGeneration {
		s.mu.Unlock()
		return errors.New("stale mirror control generation")
	}
	s.mu.Unlock()
	if old != 0 && old != binding.Generation {
		s.unbind(old)
	}
	s.mu.Lock()
	if !s.bound || s.generation != binding.Generation {
		if s.cancelBinding != nil {
			s.cancelBinding()
		}
		bound, cancel := context.WithCancel(context.Background())
		s.bindingContext = bound
		s.cancelBinding = cancel
	}
	s.generation = binding.Generation
	s.serverGeneration = binding.ServerGeneration
	s.bound = true
	s.bindingDeadline = time.Now().Add(8 * time.Second)
	s.mu.Unlock()
	s.events.setGeneration(binding.Generation, true)
	d := s.daemon
	d.mu.Lock()
	removed := []string{}
	for id := range d.runtimeIndex {
		if !seen[id] {
			removed = append(removed, id)
		}
	}
	d.runtimeIndex = map[string]Runtime{}
	d.workspaces = map[string]*workspaceState{}
	for _, resource := range binding.Resources {
		d.runtimeIndex[resource.RuntimeID] = Runtime{ID: resource.RuntimeID}
		ws := d.workspaces[resource.WorkspaceID]
		if ws == nil {
			ws = &workspaceState{workspaceID: resource.WorkspaceID}
			d.workspaces[resource.WorkspaceID] = ws
		}
		ws.runtimeIDs = append(ws.runtimeIDs, resource.RuntimeID)
	}
	d.mirrorControlGeneration = mirrorControlGeneration(binding.Generation)
	d.vscreenServerGeneration = binding.ServerGeneration
	d.mu.Unlock()
	for _, runtimeID := range removed {
		if err := s.removeRuntime(context.Background(), runtimeID); err != nil {
			s.unbind(binding.Generation)
			return err
		}
	}
	d.replayActiveMirrorViewerStates(s.enqueue(binding.Generation), mirrorControlGeneration(binding.Generation))
	return nil
}
func (s *mirrorProcessService) unbind(generation uint64) {
	s.mu.Lock()
	if !s.bound || s.generation != generation {
		s.mu.Unlock()
		return
	}
	s.bound = false
	if s.cancelBinding != nil {
		s.cancelBinding()
	}
	executions := make([]*mirrorChildExecution, 0, len(s.executions))
	for _, execution := range s.executions {
		if !execution.released {
			executions = append(executions, execution)
		}
	}
	s.mu.Unlock()
	s.events.setGeneration(generation, false)
	d := s.daemon
	d.mu.Lock()
	d.mirrorControlGeneration = 0
	backend := d.controlBackend
	d.mu.Unlock()
	if backend != nil {
		backend.mu.Lock()
		backend.catalog = map[string]cachedCatalog{}
		backend.mu.Unlock()
	}
	for _, tracked := range d.trackedRuntimeMirrors() {
		tracked.mirror.RevokeAllControl(generation)
	}
	d.suspendVscreens(0)
	for _, execution := range executions {
		execution.cancel()
		if execution.execution.stopProvider != nil && (execution.execution.nativeGranted.Load() || execution.claim.Source != nil || execution.claim.Continuation != nil) {
			execution.execution.stopProvider(&vscreen.Error{Reason: protocol.VscreenNativeUnavailable, Cause: errVscreenIntervention})
		}
	}
}
func (s *mirrorProcessService) submit(request runtimeproc.Request, input mirrorProcessRequest) (json.RawMessage, *runtimeproc.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.bound || input.Generation != s.generation {
		return nil, mirrorIPCError(errors.New("mirror control binding unavailable"))
	}
	if len(s.jobs) >= 64 {
		return nil, mirrorIPCError(errors.New("mirror operation capacity exhausted"))
	}
	ctx, cancel := context.WithDeadline(s.bindingContext, request.Deadline)
	job := &mirrorProcessJob{generation: input.Generation, input: input, cancel: cancel, expires: request.Deadline.Add(time.Minute), result: mirrorJobResult{JobID: request.RequestID}}
	s.jobs[request.RequestID] = job
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		defer cancel()
		result, err := s.execute(ctx, input)
		if ctx.Err() != nil {
			err = errors.Join(err, ctx.Err(), s.cleanupJobResult(input, result))
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		job.result.Done = true
		job.result.Result = result
		if err != nil {
			job.result.Error = err.Error()
			job.result.Reason = vscreenReason(err)
		}
	}()
	return marshalRaw(job.result), nil
}
func (s *mirrorProcessService) execute(ctx context.Context, input mirrorProcessRequest) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d := s.daemon
	g := mirrorControlGeneration(input.Generation)
	switch input.Operation {
	case "control":
		var message protocol.Message
		if json.Unmarshal(input.Payload, &message) != nil {
			return nil, errors.New("invalid mirror control message")
		}
		msg := mirrorOfferMessage{raw: message.Payload, enqueue: s.enqueue(input.Generation), controlGeneration: g}
		switch message.Type {
		case protocol.EventMirrorOffer:
			d.handleMirrorOffer(s.bindingFor(input.Generation), msg)
		case protocol.EventMirrorViewerRenew:
			d.handleVscreenViewerRenew(msg)
		case protocol.EventMirrorViewerRevoke:
			d.handleVscreenViewerRevoke(msg)
		case protocol.EventMirrorControlGrant:
			d.handleMirrorControlGrant(msg)
		case protocol.EventMirrorControlRevoke:
			d.handleMirrorControlRevoke(msg)
		case protocol.EventVscreenQuery:
			d.handleVscreenQuery(ctx, msg)
		case protocol.EventVscreenCommand:
			d.handleVscreenCommand(ctx, msg)
		case protocol.EventVscreenResult:
			d.handleVscreenCleanupAck(msg)
		default:
			return nil, errors.New("unsupported mirror control event")
		}
		return marshalRaw(struct{}{}), nil
	case "human_get":
		return marshalRaw(map[string]bool{"enabled": d.HumanInteractionEnabled()}), nil
	case "human_set":
		var value struct {
			Enabled bool `json:"enabled"`
		}
		if json.Unmarshal(input.Payload, &value) != nil {
			return nil, errors.New("invalid interaction setting")
		}
		d.SetHumanInteractionEnabled(value.Enabled)
		return marshalRaw(struct{}{}), nil
	case "execute_command":
		var command protocol.VscreenCommand
		if json.Unmarshal(input.Payload, &command) != nil {
			return nil, errors.New("invalid mirror command")
		}
		return marshalRaw(struct{}{}), d.executeVscreenCommand(ctx, command, g)
	case "snapshot":
		value, err := d.vscreenSnapshot(ctx, input.WorkspaceID, input.RuntimeID)
		return marshalRaw(value), err
	case "sources":
		value, err := d.vscreenSources(ctx, input.WorkspaceID, input.RuntimeID)
		return marshalRaw(value), err
	case "remove":
		return marshalRaw(struct{}{}), s.removeRuntime(ctx, input.RuntimeID)
	case "acquire":
		return s.acquireExecution(ctx, input)
	case "cancel_execution":
		s.mu.Lock()
		execution := s.executions[input.ExecutionID]
		if execution == nil || input.Claim == nil || !mirrorClaimsEqual(execution.claim, *input.Claim) {
			s.mu.Unlock()
			return nil, errors.New("GUI cancellation claim differs")
		}
		s.mu.Unlock()
		execution.cancel()
		return marshalRaw(struct{}{}), nil
	case "release":
		return s.releaseExecution(input)
	case "provider_stopped":
		return s.providerStopped(ctx, input)
	case "local":
		if input.Local == nil {
			return nil, errors.New("local action required")
		}
		value, err := d.performMirrorLocal(ctx, *input.Local)
		return marshalRaw(value), err
	case "approval":
		if input.Approval == nil {
			return nil, errors.New("approval request required")
		}
		d.mu.Lock()
		rm := d.runtimeMirrors[input.Approval.RuntimeID]
		d.mu.Unlock()
		if rm == nil {
			return nil, errors.New("no mirror reviewer connected")
		}
		a := input.Approval
		approved, err := rm.RequestCLIApproval(ctx, mirror.CLIApprovalAudience{WorkspaceID: a.WorkspaceID, RuntimeID: a.RuntimeID, UserID: a.UserID}, "Codex operation approval", a.Operation.Target, &a.Operation)
		return marshalRaw(map[string]bool{"approved": approved}), err
	}
	return nil, errors.New("unknown mirror domain operation")
}
func (s *mirrorProcessService) bindingFor(generation uint64) context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bound && s.generation == generation {
		return s.bindingContext
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
func (s *mirrorProcessService) enqueue(generation uint64) func([]byte) (*wsOutbound, error) {
	return func(frame []byte) (*wsOutbound, error) {
		_, id, err := s.events.emit(context.Background(), generation, "outbound", json.RawMessage(frame))
		if err != nil {
			return nil, err
		}
		return &wsOutbound{cancelRemote: func() bool {
			result, _, err := s.events.emit(context.Background(), generation, "cancel_outbound", map[string]string{"id": id})
			if err != nil {
				return false
			}
			var value struct {
				Cancelled bool `json:"cancelled"`
			}
			return json.Unmarshal(result, &value) == nil && value.Cancelled
		}}, nil
	}
}
func mirrorIPCError(err error) *runtimeproc.Error {
	if err == nil {
		return nil
	}
	return &runtimeproc.Error{Code: string(vscreenReason(err)), Message: err.Error()}
}
func (s *mirrorProcessService) shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		err := s.shutdownErr
		s.mu.Unlock()
		return err
	}
	s.shutdownErr = errors.New("mirror shutdown unconfirmed")
	s.closed = true
	s.bound = false
	if s.cancelBinding != nil {
		s.cancelBinding()
	}
	for _, job := range s.jobs {
		job.cancel()
	}
	executions := make([]*mirrorChildExecution, 0, len(s.executions))
	for _, execution := range s.executions {
		executions = append(executions, execution)
	}
	s.mu.Unlock()
	close(s.stop)
	<-s.stopped
	var result error
	if s.events != nil {
		result = errors.Join(result, s.events.close())
	}
	for _, execution := range executions {
		execution.broker.Close()
		execution.cancel()
		execution.execution.Close()
	}
	s.workers.Wait()
	d := s.daemon
	d.mu.Lock()
	mirrors := d.runtimeMirrors
	d.runtimeMirrors = map[string]*mirror.RuntimeMirror{}
	d.mu.Unlock()
	for _, runtimeMirror := range mirrors {
		result = errors.Join(result, runtimeMirror.Close(ctx))
	}
	result = errors.Join(result, d.closeVscreensResult())
	if result == nil {
		result = s.writeShutdownProof()
	}
	s.nativeWorkers.Wait()
	s.notifications.Wait()
	s.mu.Lock()
	s.shutdownErr = result
	s.mu.Unlock()
	return result
}
func (s *mirrorProcessService) watchNative(client *hostclient.Client) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.nativeWorkers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.nativeWorkers.Done()
		select {
		case <-s.stop:
			return
		case <-client.Done():
		}
		s.mu.Lock()
		closed, generation := s.closed, s.generation
		s.mu.Unlock()
		if closed {
			return
		}
		s.unbind(generation)
		s.notifySafety(generation, "native_fault", map[string]string{"reason": "native_channel_lost"})
	}()
	return true
}

func (s *mirrorProcessService) removeRuntime(ctx context.Context, runtimeID string) error {
	s.mu.Lock()
	executions := map[string]*mirrorChildExecution{}
	for id, execution := range s.executions {
		if execution.claim.RuntimeID == runtimeID {
			executions[id] = execution
		}
	}
	s.mu.Unlock()
	for id, execution := range executions {
		if execution.execution.stopProvider != nil {
			execution.execution.stopProvider(&vscreen.Error{Reason: protocol.VscreenSourceGone, Cause: errVscreenIntervention})
		}
		if _, err := s.releaseExecution(mirrorProcessRequest{ExecutionID: id, Claim: &execution.claim}); err != nil {
			return err
		}
	}
	d := s.daemon
	d.mu.Lock()
	runtimeMirror := d.runtimeMirrors[runtimeID]
	delete(d.runtimeMirrors, runtimeID)
	d.mu.Unlock()
	if runtimeMirror != nil {
		if err := runtimeMirror.Close(ctx); err != nil {
			return err
		}
	}
	d.vscreenMu.Lock()
	runtime := d.vscreen
	d.vscreenMu.Unlock()
	if runtime != nil {
		return d.removeVscreenRuntime(ctx, runtime, runtimeID)
	}
	return nil
}
func (s *mirrorProcessService) inventory() json.RawMessage {
	s.mu.Lock()
	bound, generation, count := s.bound, s.generation, len(s.executions)
	s.mu.Unlock()
	d := s.daemon
	d.mu.Lock()
	mirrors := map[string]bool{}
	for id, rm := range d.runtimeMirrors {
		mirrors[id] = rm.HasViewers()
	}
	d.mu.Unlock()
	d.vscreenMu.Lock()
	runtime := d.vscreen
	d.vscreenMu.Unlock()
	nativeAlive := false
	if runtime != nil {
		runtime.mu.Lock()
		client := runtime.client
		runtime.mu.Unlock()
		if client != nil {
			select {
			case <-client.Done():
			default:
				nativeAlive = true
			}
		}
	}
	return marshalRaw(map[string]any{"pid": os.Getpid(), "instance_id": s.bootstrap.Identity.InstanceID, "bound": bound, "generation": generation, "executions": count, "mirrors": mirrors, "native_alive": nativeAlive})
}

func (s *mirrorProcessService) cleanupJobResult(input mirrorProcessRequest, result json.RawMessage) error {
	if input.Operation == "control" {
		var frame protocol.Message
		var offer protocol.MirrorOfferPayload
		if json.Unmarshal(input.Payload, &frame) == nil && frame.Type == protocol.EventMirrorOffer && json.Unmarshal(frame.Payload, &offer) == nil && offer.ViewerGrant != nil {
			grant := offer.ViewerGrant
			revoke := protocol.MirrorViewerRevokePayload{WorkspaceID: offer.WorkspaceID, RuntimeID: offer.RuntimeID, DaemonGeneration: offer.DaemonGeneration, SessionID: offer.SessionID, ViewerID: offer.ViewerID, GrantID: grant.GrantID}
			s.daemon.handleVscreenViewerRevoke(mirrorOfferMessage{raw: marshalRaw(revoke), controlGeneration: mirrorControlGeneration(input.Generation)})
		}
		return nil
	}
	if input.Operation != "acquire" {
		return nil
	}
	var grant mirrorExecutionGrant
	if json.Unmarshal(result, &grant) != nil || !grant.Active {
		return nil
	}
	_, err := s.releaseExecution(mirrorProcessRequest{ExecutionID: grant.ExecutionID, Claim: input.Claim})
	return err
}

func (s *mirrorProcessService) notifySafety(generation uint64, kind string, payload any) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.notifications.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.notifications.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _, _ = s.events.emit(ctx, generation, kind, payload)
	}()
}
