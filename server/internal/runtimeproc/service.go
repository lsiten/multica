package runtimeproc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Config injects actual domain capabilities. Ready must finish dependency checks;
// Shutdown must join domain workers and release resources before reporting success.
// Both callbacks and Handler must cooperate with cancellation.
type Config struct {
	Bootstrap    Bootstrap
	Capabilities []string
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
func isBuiltin(op string) bool {
	return slices.Contains([]string{"handshake", "health", "query_operation", "drain", "resume", "stop", "acknowledge"}, op)
}
func (s *Service) statusLocked() Status {
	return Status{ReplayEpoch: s.record.ReplayEpoch, Identity: s.record.Identity, State: s.record.State, Fence: s.record.Fence, Capabilities: append([]string{"handshake", "health", "query_operation", "drain", "resume", "stop", "acknowledge"}, append(slices.Clone(s.config.Capabilities), s.config.ReadCapabilities...)...)}
}
func (s *Service) respond(w http.ResponseWriter, code int, receipt *Receipt, problem *Error) {
	s.mu.Lock()
	status := s.statusLocked()
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// Broken connections do not undo durable operation receipts.
	_ = json.NewEncoder(w).Encode(Response{Status: status, Receipt: receipt, Error: problem})
}
func (s *Service) reject(w http.ResponseWriter, code int, kind, message string) {
	s.respond(w, code, nil, &Error{Code: kind, Message: message})
}
func (s *Service) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.config.Bootstrap.Token)) != 1 {
		s.reject(w, 401, "unauthorized", "private credential required")
		return
	}
	if r.Method != "POST" || r.URL.Path != "/rpc" || r.URL.RawQuery != "" {
		s.reject(w, 404, "not_found", "unknown endpoint")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		s.reject(w, 413, "payload_too_large", "request exceeds limit")
		return
	}
	var req Request
	if json.Unmarshal(raw, &req) != nil {
		s.reject(w, 400, "malformed", "invalid request body")
		return
	}
	if req.Identity != s.config.Bootstrap.Identity {
		s.reject(w, 409, "identity_mismatch", "scope instance build or protocol differs")
		return
	}
	if len(req.RequestID) == 0 || len(req.RequestID) > 128 || len(req.Operation) > 128 {
		s.reject(w, 400, "malformed", "request identity required")
		return
	}
	if req.Deadline.IsZero() || !req.Deadline.After(time.Now()) || req.Deadline.After(time.Now().Add(5*time.Minute)) {
		s.reject(w, 408, "deadline", "deadline expired or exceeds five minutes")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), req.Deadline)
	defer cancel()
	if req.Operation == "handshake" || req.Operation == "health" {
		s.respond(w, 200, nil, nil)
		return
	}
	if req.Operation == "query_operation" {
		var query struct {
			RequestID string `json:"request_id"`
		}
		if json.Unmarshal(req.Payload, &query) != nil || query.RequestID == "" {
			s.reject(w, 400, "malformed", "query request id required")
			return
		}
		s.mu.Lock()
		receipt, ok := s.record.Operations[query.RequestID]
		replayEpoch := s.record.ReplayEpoch
		s.mu.Unlock()
		if !ok && !strings.HasPrefix(query.RequestID, fmt.Sprintf("%d:", replayEpoch)) {
			s.reject(w, 410, "retired_request", "operation belongs to a retired replay epoch")
			return
		}
		if !ok {
			s.reject(w, 404, "not_found", "operation has not been accepted")
			return
		}
		s.respond(w, 200, &receipt, nil)
		return
	}
	if slices.Contains(s.config.ReadCapabilities, req.Operation) {
		s.mu.Lock()
		ready := s.record.State == "ready" || s.record.State == "draining"
		s.mu.Unlock()
		if !ready {
			s.reject(w, 409, "not_ready", "service is not available")
			return
		}
		result, problem := s.config.ReadHandler(ctx, req)
		if ctx.Err() != nil {
			s.reject(w, 408, "deadline", "read deadline exceeded")
			return
		}
		if len(result) > maxBody || len(result) > 0 && !json.Valid(result) {
			s.reject(w, 500, "invalid_result", "invalid readonly result")
			return
		}
		s.respond(w, 200, &Receipt{RequestID: req.RequestID, State: "read", Result: result, Error: problem}, problem)
		return
	}
	if !isBuiltin(req.Operation) && !slices.Contains(s.config.Capabilities, req.Operation) {
		s.reject(w, 400, "unknown_operation", "unsupported operation")
		return
	}
	release, admissionErr := s.admit(ctx, req.Operation)
	if admissionErr != nil {
		s.reject(w, 408, "deadline", "operation was not admitted")
		return
	}
	defer release()
	if ctx.Err() != nil {
		s.reject(w, 408, "deadline", "operation was not admitted")
		return
	}
	// Hash the full typed request, including authorization fences and deadline.
	canonical, _ := json.Marshal(req)
	digest := sha256.Sum256(canonical)
	fingerprint := hex.EncodeToString(digest[:])
	s.mu.Lock()
	if ctx.Err() != nil || !req.Deadline.After(time.Now()) {
		s.mu.Unlock()
		s.reject(w, 408, "deadline", "operation was not admitted")
		return
	}
	if prior, ok := s.record.Operations[req.RequestID]; ok {
		s.mu.Unlock()
		if prior.Digest != fingerprint {
			s.reject(w, 409, "request_conflict", "request id belongs to different content")
			return
		}
		s.respond(w, 200, &prior, nil)
		return
	}
	if !strings.HasPrefix(req.RequestID, fmt.Sprintf("%d:", s.record.ReplayEpoch)) {
		s.mu.Unlock()
		s.reject(w, 409, "retired_request", "request id belongs to another replay epoch")
		return
	}
	if req.ReplayEpoch != s.record.ReplayEpoch {
		s.mu.Unlock()
		s.reject(w, 409, "retired_request", "replay epoch differs")
		return
	}
	if req.Fence != s.record.Fence {
		s.mu.Unlock()
		s.reject(w, 409, "stale_fence", "supervisor resource epoch or revision differs")
		return
	}
	if s.record.State != "ready" && req.Operation != "resume" && req.Operation != "stop" && req.Operation != "acknowledge" {
		s.mu.Unlock()
		s.reject(w, 409, "draining", "service is not admitting mutations")
		return
	}
	if s.record.State == "stopped" {
		s.mu.Unlock()
		s.reject(w, 409, "stopped", "service has stopped")
		return
	}
	if req.Operation == "acknowledge" {
		for _, prior := range s.record.Operations {
			if prior.State != "completed" {
				s.mu.Unlock()
				s.reject(w, 409, "uncertain_operation", "pending receipts cannot be retired")
				return
			}
		}
	}
	limit := maxOperations
	if isBuiltin(req.Operation) {
		limit += 16
	}
	if req.Operation == "stop" {
		limit++
	}
	if len(s.record.Operations) >= limit && req.Operation != "acknowledge" {
		s.mu.Unlock()
		s.reject(w, 507, "resource_exhausted", "receipt quota reached; drain this instance")
		return
	}
	if !isBuiltin(req.Operation) {
		current, _ := json.Marshal(s.record)
		reserved := maxBody
		for _, pending := range s.record.Operations {
			if pending.State == "pending" {
				reserved += maxBody
			}
		}
		if len(current)+reserved > maxJournal-(1<<20) {
			s.mu.Unlock()
			s.reject(w, 507, "resource_exhausted", "journal quota reached; acknowledge completed receipts")
			return
		}
	}
	receipt := Receipt{RequestID: req.RequestID, Digest: fingerprint, State: "pending", Fence: s.record.Fence}
	s.record.Operations[req.RequestID] = receipt
	if err = writeRecord(s.path, s.record); err != nil {
		delete(s.record.Operations, req.RequestID)
		s.mu.Unlock()
		s.reject(w, 507, "storage", "operation intent was not persisted")
		return
	}
	s.mu.Unlock()
	if ctx.Err() != nil || !req.Deadline.After(time.Now()) {
		s.respond(w, 200, &receipt, nil)
		return
	}
	var result json.RawMessage
	var problem *Error
	switch req.Operation {
	case "drain", "resume", "acknowledge":
	case "stop":
		if s.config.Shutdown != nil {
			if err = s.config.Shutdown(ctx); err != nil {
				problem = &Error{Code: "shutdown_failed", Message: "domain shutdown remains unconfirmed"}
			}
		}
	default:
		result, problem = s.config.Handler(ctx, req)
	}
	if len(result) > maxBody || len(result) > 0 && !json.Valid(result) {
		result = nil
		problem = &Error{Code: "invalid_result", Message: "domain result is invalid or exceeds limit"}
	}
	if problem != nil && (len(problem.Code) > 128 || len(problem.Message) > 1024) {
		problem = &Error{Code: "domain_failure", Message: "domain failure exceeds transport limit"}
	}
	// A canceled call may already have external effects; retain uncertainty.
	if ctx.Err() != nil {
		s.respond(w, 200, &receipt, nil)
		return
	}
	s.mu.Lock()
	receipt.Result = bytes.Clone(result)
	receipt.Error = problem
	receipt.State = "completed"
	s.record.Fence.Revision++
	if problem == nil {
		switch req.Operation {
		case "drain":
			s.record.State = "draining"
		case "resume":
			s.record.State = "ready"
		case "stop":
			s.record.State = "stopped"
		}
	}
	previousOperations := s.record.Operations
	previousReplayEpoch := s.record.ReplayEpoch
	if req.Operation == "acknowledge" && problem == nil {
		s.record.ReplayEpoch++
		s.record.Operations = make(map[string]Receipt)
	}
	receipt.Fence = s.record.Fence
	s.record.Operations[req.RequestID] = receipt
	if err = writeRecord(s.path, s.record); err != nil {
		// Never expose an undurable success even to a later duplicate/query.
		s.record.Operations = previousOperations
		s.record.ReplayEpoch = previousReplayEpoch
		s.record.Operations[req.RequestID] = Receipt{RequestID: req.RequestID, Digest: fingerprint, State: "pending", Fence: req.Fence}
		s.record.State = "suspect"
		s.mu.Unlock()
		s.reject(w, 507, "storage", "operation outcome could not be persisted")
		return
	}
	s.mu.Unlock()
	s.respond(w, 200, &receipt, nil)
	if req.Operation == "stop" && problem == nil {
		s.stopOnce.Do(func() { close(s.stop) })
	}
}

// admit never holds the journal mutex while waiting or running a domain handler.
// Lifecycle writers first exclude new ordinary work. While an already admitted
// ordinary handler remains active, confirmations may enter to release its domain
// guard. Once it returns, writer preference closes confirmation admission too.
// This dependency window is necessary: a conventional writer-preferring RWMutex
// would deadlock stop -> ordinary -> confirmation. Every wait is caller bounded.
func (s *Service) admit(ctx context.Context, operation string) (func(), error) {
	lifecycle := isBuiltin(operation)
	concurrent := slices.Contains(s.config.ConcurrentCapabilities, operation)
	s.admission.Lock()
	if lifecycle {
		s.lifecycleWaiting++
	}
	for {
		if err := ctx.Err(); err != nil {
			if lifecycle {
				s.lifecycleWaiting--
				s.signalAdmissionLocked()
			}
			s.admission.Unlock()
			return nil, err
		}
		active := s.concurrentActive
		if s.ordinaryActive {
			active++
		}
		allowed := false
		switch {
		case lifecycle:
			allowed = !s.lifecycleActive && active == 0
		case concurrent:
			allowed = !s.lifecycleActive && active < 4 && (s.lifecycleWaiting == 0 || s.ordinaryActive)
		default:
			allowed = !s.lifecycleActive && !s.ordinaryActive && active < 4 && s.lifecycleWaiting == 0
		}
		if allowed {
			switch {
			case lifecycle:
				s.lifecycleWaiting--
				s.lifecycleActive = true
			case concurrent:
				s.concurrentActive++
			default:
				s.ordinaryActive = true
			}
			s.admission.Unlock()
			return func() {
				s.admission.Lock()
				switch {
				case lifecycle:
					s.lifecycleActive = false
				case concurrent:
					s.concurrentActive--
				default:
					s.ordinaryActive = false
				}
				s.signalAdmissionLocked()
				s.admission.Unlock()
			}, nil
		}
		changed := s.admissionChanged
		s.admission.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		s.admission.Lock()
	}
}
func (s *Service) signalAdmissionLocked() {
	close(s.admissionChanged)
	s.admissionChanged = make(chan struct{})
}
