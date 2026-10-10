package modelservice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/jevmodels"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

const leaseLifetime = 90 * time.Second
const maxLeases = 128

// Service owns model state, install jobs, and lease-scoped inference grants.
type Service struct {
	bootstrap    runtimeproc.Bootstrap
	manager      Manager
	mu           sync.Mutex
	inventory    inventory
	leases       map[string]*leaseState
	proxy        *http.Server
	proxyAddress string
	proxyDone    chan error
	cancel       context.CancelFunc
	joined       chan struct{}
	installs     sync.WaitGroup
	requests     sync.WaitGroup
	closed       bool
	shutdownErr  error
	leaseTTL     time.Duration
	acquisitions map[string]*acquisition
	acquiring    sync.WaitGroup
}
type leaseState struct {
	grant    grant
	model    ModelLease
	inflight int
	revoked  bool
}

func newService(b runtimeproc.Bootstrap, m Manager) (*Service, error) {
	inv, err := readInventory(b.Root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if inv.DescendantsPossible {
		return nil, errors.New("model descendants from prior owner are unconfirmed")
	}
	if inv.Jobs == nil {
		inv.Jobs = map[string]InstallJob{}
	}
	for key, job := range inv.Jobs {
		if activeJob(job.State) {
			job.State = "interrupted"
			job.Error = "prior install was interrupted; explicit retry required"
			job.UpdatedAt = time.Now()
			inv.Jobs[key] = job
		}
	}
	inv.InstanceID = b.Identity.InstanceID
	if err = saveInventory(b.Root, inv); err != nil {
		return nil, err
	}
	return &Service{bootstrap: b, manager: m, inventory: inv, leases: map[string]*leaseState{}, joined: make(chan struct{}), leaseTTL: leaseLifetime, acquisitions: map[string]*acquisition{}}, nil
}

// Run creates the real manager in the child; bootstrap contains no account credential.
func Run(ctx context.Context, b runtimeproc.Bootstrap) error {
	if b.Identity.Scope.Service != "ai" {
		return errors.New("unsupported model service role")
	}
	manager, err := jevmodels.New(ctx, jevmodels.Config{RootDir: b.Root, PythonPath: os.Getenv("MULTICA_JEV_PYTHON"), PersistInstall: true})
	if err != nil {
		return err
	}
	service, err := newService(b, realManager{manager})
	if err != nil {
		manager.Close()
		return err
	}
	return service.serve(ctx)
}
func (s *Service) serve(ctx context.Context) error {
	transport, err := runtimeproc.NewService(runtimeproc.Config{Bootstrap: s.bootstrap, Capabilities: []string{"model.register", "model.install", "model.cancel", "model.stop", "model.remove", "model.acquire", "model.keepalive", "model.release", "model.acquire_cancel", "model.acquire_ack"}, ReadCapabilities: []string{"model.catalog", "model.status", "model.job", "model.acquire_status"}, Handler: s.mutate, ReadHandler: s.read, Ready: s.ready, Shutdown: s.shutdown})
	if err != nil {
		return errors.Join(err, s.manager.Close())
	}
	return transport.Serve(ctx)
}
func (s *Service) ready(ctx context.Context) error {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.proxyAddress = "http://" + listener.Addr().String()
	life, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.proxy = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		defer s.requests.Done()
		s.infer(w, r)
	}), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return life }}
	s.proxyDone = make(chan error, 1)
	go func() { s.proxyDone <- s.proxy.Serve(listener) }()
	go func() {
		defer close(s.joined)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-life.Done():
				return
			case <-tick.C:
				s.expire()
			}
		}
	}()
	return nil
}
func (s *Service) shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		err := s.shutdownErr
		s.mu.Unlock()
		return err
	}
	s.closed = true
	for _, job := range s.acquisitions {
		job.cancel()
	}
	s.shutdownErr = errors.New("model shutdown incomplete")
	s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		<-s.joined
	}
	if s.proxy != nil {
		if err := s.proxy.Shutdown(ctx); err != nil {
			s.proxy.Close()
		}
		<-s.proxyDone
		s.requests.Wait()
	}
	// Quiesce before publishing the clean receipt, retaining the real cache lock.
	err := s.manager.CloseWithReceipt(func() error {
		s.acquiring.Wait()
		s.installs.Wait()
		s.mu.Lock()
		defer s.mu.Unlock()
		for id, lease := range s.leases {
			lease.model.Release()
			delete(s.leases, id)
		}
		s.inventory.DescendantsPossible = false
		return saveInventory(s.bootstrap.Root, s.inventory)
	})
	s.mu.Lock()
	s.shutdownErr = err
	s.mu.Unlock()
	return err
}
func decodeRequest(req runtimeproc.Request) (modelRequest, error) {
	var input modelRequest
	if len(req.Payload) == 0 {
		return input, nil
	}
	if err := json.Unmarshal(req.Payload, &input); err != nil {
		return input, errors.New("invalid model request")
	}
	return input, nil
}
func (s *Service) read(ctx context.Context, req runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	input, err := decodeRequest(req)
	if err != nil {
		return nil, problem(err)
	}
	if err = ctx.Err(); err != nil {
		return nil, problem(err)
	}
	switch req.Operation {
	case "model.acquire_status":
		return s.acquisitionStatus(input)
	case "model.catalog":
		return encode(s.manager.Catalog())
	case "model.status":
		status, err := s.manager.Status(input.ModelID, input.Revision)
		if err != nil {
			return nil, problem(err)
		}
		s.mu.Lock()
		job, ok := s.inventory.Jobs[input.ModelID+"@"+input.Revision]
		s.mu.Unlock()
		if ok && !status.Installed && !activeJob(job.State) && (job.State == "cancelled" || job.State == "interrupted") {
			status.State = job.State
			status.Error = job.Error
		}
		return encode(status)
	case "model.job":
		s.mu.Lock()
		job, ok := s.inventory.Jobs[input.ModelID+"@"+input.Revision]
		s.mu.Unlock()
		if !ok {
			return nil, &runtimeproc.Error{Code: "not_found", Message: "no install job"}
		}
		return encode(job)
	}
	return nil, &runtimeproc.Error{Code: "unknown_operation", Message: "unknown model read"}
}
func (s *Service) mutate(ctx context.Context, req runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	input, err := decodeRequest(req)
	if err != nil {
		return nil, problem(err)
	}
	switch req.Operation {
	case "model.register":
		model, err := s.manager.Register(ctx, input.ModelID, input.Revision, nil)
		if err != nil {
			return nil, problem(err)
		}
		return encode(model)
	case "model.install":
		return s.install(input, req.RequestID)
	case "model.cancel":
		return s.cancelInstall(input)
	case "model.stop":
		return encodeError(s.manager.Stop(input.ModelID, input.Revision))
	case "model.remove":
		return encodeError(s.manager.Remove(input.ModelID, input.Revision))
	case "model.acquire":
		return s.startAcquire(req, input)
	case "model.acquire_cancel", "model.acquire_ack":
		return s.finishAcquire(req.Operation, input)
	case "model.keepalive", "model.release":
		return s.updateLease(req.Operation, input)
	}
	return nil, &runtimeproc.Error{Code: "unknown_operation", Message: "unknown model mutation"}
}
func encodeError(err error) (json.RawMessage, *runtimeproc.Error) {
	if err != nil {
		return nil, problem(err)
	}
	return encode(struct{}{})
}
func (s *Service) markDescendants() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return jevmodels.ErrClosed
	}
	s.inventory.DescendantsPossible = true
	return saveInventory(s.bootstrap.Root, s.inventory)
}
func (s *Service) install(input modelRequest, id string) (json.RawMessage, *runtimeproc.Error) {
	status, err := s.manager.Status(input.ModelID, input.Revision)
	if err != nil {
		return nil, problem(err)
	}
	if status.Installed {
		return encode(status)
	}
	if input.Revision == "" {
		input.Revision = status.Revision
	}
	key := input.ModelID + "@" + input.Revision
	s.mu.Lock()
	if old, ok := s.inventory.Jobs[key]; ok && activeJob(old.State) {
		s.mu.Unlock()
		return nil, problem(jevmodels.ErrBusy)
	}
	if len(s.inventory.Jobs) >= 65 {
		if _, ok := s.inventory.Jobs[key]; !ok {
			s.mu.Unlock()
			return nil, problem(jevmodels.ErrBusy)
		}
	}
	job := InstallJob{ID: id, ModelID: input.ModelID, Revision: input.Revision, State: "queued", UpdatedAt: time.Now()}
	s.inventory.Jobs[key] = job
	s.inventory.DescendantsPossible = true
	if err = saveInventory(s.bootstrap.Root, s.inventory); err != nil {
		s.mu.Unlock()
		return nil, problem(err)
	}
	s.mu.Unlock()
	// Domain lifetime, not the short IPC admission request, owns the installation.
	result, err := s.manager.StartInstall(context.Background(), input.ModelID, input.Revision)
	if err != nil {
		s.finishJob(key, id, err)
		return nil, problem(err)
	}
	s.installs.Add(1)
	go func() {
		defer s.installs.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case err := <-result:
				s.finishJob(key, id, err)
				return
			case <-ticker.C:
				s.refreshJob(key, id, input)
			}
		}
	}()
	return encode(job)
}
func (s *Service) refreshJob(key, id string, input modelRequest) {
	status, err := s.manager.Status(input.ModelID, input.Revision)
	if err != nil {
		return
	}
	if !activeJob(status.State) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.inventory.Jobs[key]
	if job.ID != id || !activeJob(job.State) || job.State == status.State {
		return
	}
	job.State = status.State
	job.UpdatedAt = time.Now()
	s.inventory.Jobs[key] = job
	if err = saveInventory(s.bootstrap.Root, s.inventory); err != nil {
		job.State = "failed"
		job.Error = "install state could not be persisted"
		s.inventory.Jobs[key] = job
	}
}
func (s *Service) finishJob(key, id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.inventory.Jobs[key]
	if job.ID != id || job.State == "cancelled" {
		return
	}
	job.State = "installed"
	if err != nil {
		job.State = "failed"
		job.Error = err.Error()
	}
	job.UpdatedAt = time.Now()
	s.inventory.Jobs[key] = job
	if persistErr := saveInventory(s.bootstrap.Root, s.inventory); persistErr != nil {
		job.State = "failed"
		job.Error = "install completion could not be persisted"
		s.inventory.Jobs[key] = job
	}
}
func (s *Service) cancelInstall(input modelRequest) (json.RawMessage, *runtimeproc.Error) {
	key := input.ModelID + "@" + input.Revision
	s.mu.Lock()
	job, ok := s.inventory.Jobs[key]
	if !ok || job.ID != input.JobID {
		s.mu.Unlock()
		return nil, &runtimeproc.Error{Code: "stale_job", Message: "install attempt changed"}
	}
	s.mu.Unlock()
	if err := s.manager.CancelInstall(input.ModelID, input.Revision); err != nil {
		return nil, problem(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job.State = "cancelled"
	job.Error = "download cancelled"
	job.UpdatedAt = time.Now()
	s.inventory.Jobs[key] = job
	return encodeError(saveInventory(s.bootstrap.Root, s.inventory))
}
func randomToken() (string, error) {
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	return hex.EncodeToString(raw), err
}
