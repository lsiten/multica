package runtimeproc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Config injects actual domain capabilities. Ready must finish dependency checks;
// Shutdown must join domain workers and release resources before reporting success.
// Both callbacks and Handler must cooperate with cancellation.
type Config struct {
	// DetachMutations keeps admitted domain work and ACK housekeeping alive after caller disconnect.
	DetachMutations bool
	// ReconcileMutation may prove a terminal physical outcome after Handler returns
	// under cancellation. It must honor its bounded context and never replay work.
	ReconcileMutation func(context.Context, Request, Outcome) (Outcome, bool)
	Bootstrap         Bootstrap
	Capabilities      []string
	// ConcurrentCapabilities opt selected mutations out of ordinary serialization.
	// At most four domain mutations run together, including the ordinary handler.
	// The full Fence is checked at durable admission. Each completed receipt gets
	// the next global Revision, irrespective of admission order; resource-specific
	// expected revisions remain the domain handler's responsibility.
	ConcurrentCapabilities []string
	ReadCapabilities       []string
	ReadHandler            Handler
	Handler                Handler
	Ready                  func(context.Context) error
	Shutdown               func(context.Context) error
}

// Service is a single owner. Serve owns its lock until all request handlers exit.
type Service struct {
	rootContext      context.Context
	config           Config
	lock             *os.File
	path             string
	mu               sync.Mutex
	admission        sync.Mutex
	admissionChanged chan struct{}
	ordinaryActive   bool
	concurrentActive int
	lifecycleWaiting int
	lifecycleActive  bool
	record           Record
	stop             chan struct{}
	stopOnce         sync.Once
}

// NewService acquires ownership without overwriting live or unconfirmed records.
// A stopped instance may be replaced; crashed/suspect records require explicit
// domain reconciliation, which this transport deliberately does not fabricate.
func NewService(config Config) (*Service, error) {
	b := config.Bootstrap
	if err := b.validate(); err != nil {
		return nil, err
	}
	if len(config.Capabilities)+len(config.ReadCapabilities) > 32 {
		return nil, errors.New("too many runtime capabilities")
	}
	for _, capability := range append(slices.Clone(config.Capabilities), config.ReadCapabilities...) {
		if capability == "" || len(capability) > 64 || isBuiltin(capability) {
			return nil, errors.New("invalid domain capability")
		}
	}
	seenConcurrent := map[string]bool{}
	for _, capability := range config.ConcurrentCapabilities {
		if isBuiltin(capability) || !slices.Contains(config.Capabilities, capability) || slices.Contains(config.ReadCapabilities, capability) || seenConcurrent[capability] {
			return nil, errors.New("concurrent capabilities must be unique registered domain mutations")
		}
		seenConcurrent[capability] = true
	}
	if len(config.ReadCapabilities) > 0 && config.ReadHandler == nil {
		return nil, errors.New("readonly capabilities require a handler")
	}
	for _, capability := range config.ReadCapabilities {
		if slices.Contains(config.Capabilities, capability) {
			return nil, errors.New("capability cannot be both read and mutation")
		}
	}
	if len(config.Capabilities) > 0 && config.Handler == nil {
		return nil, errors.New("capabilities require a handler")
	}
	dir, err := prepareDirectory(b.Root, b.Identity.Scope)
	if err != nil {
		return nil, err
	}
	lock, err := lockFile(filepath.Join(dir, "owner.lock"))
	if err != nil {
		return nil, errors.New("runtime owner is live or unknown")
	}
	path := RecordPath(b.Root, b.Identity.Scope)
	if raw, readErr := readPrivate(path); readErr == nil {
		if !replaceableRecord(raw, b.Identity) {
			lock.Close()
			return nil, errors.New("runtime prior owner requires reconciliation")
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		lock.Close()
		return nil, readErr
	}
	s := &Service{admissionChanged: make(chan struct{}), config: config, lock: lock, path: path, stop: make(chan struct{}), record: Record{ReplayEpoch: 1, Identity: b.Identity, Token: b.Token, State: "starting", Fence: b.Fence, Operations: make(map[string]Receipt)}}
	s.config.Capabilities = slices.Clone(config.Capabilities)
	s.config.ConcurrentCapabilities = slices.Clone(config.ConcurrentCapabilities)
	s.config.ReadCapabilities = slices.Clone(config.ReadCapabilities)
	if err = writeRecord(path, s.record); err != nil {
		lock.Close()
		return nil, err
	}
	return s, nil
}

// Serve listens only on an ephemeral numeric loopback port. Root cancellation
// cancels all requests and joins them before releasing ownership. Domain callbacks
// that ignore cancellation prevent graceful exit; the owning Process can kill it.
func (s *Service) Serve(ctx context.Context) (resultErr error) {
	defer s.lock.Close()
	defer func() {
		s.mu.Lock()
		stopped := s.record.State == "stopped"
		s.mu.Unlock()
		if !stopped && s.config.Shutdown != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
			cleanupErr := s.config.Shutdown(cleanupCtx)
			cleanupCancel()
			if cleanupErr != nil {
				resultErr = errors.Join(resultErr, cleanupErr)
				return
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.record.State = "stopped"
		resultErr = errors.Join(resultErr, writeRecord(s.path, s.record))
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.rootContext = ctx
	s.mu.Unlock()
	if s.config.Ready != nil {
		if err := s.config.Ready(ctx); err != nil {
			return err
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	s.mu.Lock()
	s.record.Address = "http://" + listener.Addr().String()
	s.record.State = "ready"
	err = writeRecord(s.path, s.record)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	var requests sync.WaitGroup
	server := &http.Server{ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return ctx }, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		defer requests.Done()
		s.serveHTTP(w, r)
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
	case <-s.stop:
	case err = <-done:
		cancel()
		server.Close()
		requests.Wait()
		return err
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	shutdownErr := server.Shutdown(shutdownCtx)
	shutdownCancel()
	if shutdownErr != nil {
		server.Close()
	}
	<-done
	requests.Wait()
	return nil
}
