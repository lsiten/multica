package applicationhost

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/processtree"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Status is the nonce-authenticated evidence used to recover ownership without adopting a PID.
type Status struct {
	HostID      string                          `json:"host_id"`
	InstanceID  string                          `json:"instance_id"`
	WorkspaceID string                          `json:"workspace_id"`
	RuntimeID   string                          `json:"runtime_id"`
	Observation protocol.ApplicationObservation `json:"observation"`
}

type host struct {
	mu            sync.Mutex
	record        Record
	path          string
	log           *boundedLog
	environment   []string
	secrets       []string
	cancelService context.CancelFunc
	serviceDone   chan struct{}
	shutdown      chan struct{}
	shutdownOnce  sync.Once
	cleanupFailed bool
}

// Environment passes only explicit application variables and basic OS plumbing to the child.
func Environment(config protocol.ApplicationConfig) ([]string, []string, error) {
	values := map[string]string{}
	for _, name := range []string{"PATH", "HOME", "USER", "TMPDIR", "TEMP", "TMP", "LANG", "LC_ALL", "SystemRoot", "SYSTEMROOT", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(name); ok {
			values[name] = value
		}
	}
	secrets := []string{}
	for name, value := range config.Environment {
		values[name] = value
		lower := strings.ToLower(name)
		if strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "key") {
			secrets = append(secrets, value)
		}
	}
	for name, source := range config.LocalEnv {
		value, ok := os.LookupEnv(source)
		if !ok {
			return nil, nil, fmt.Errorf("local environment reference %s is unavailable", source)
		}
		values[name] = value
		secrets = append(secrets, value)
	}
	if config.Port > 0 {
		if _, set := values["PORT"]; !set {
			values["PORT"] = strconv.Itoa(config.Port)
		}
	}
	environment := make([]string, 0, len(values))
	for name, value := range values {
		environment = append(environment, name+"="+value)
	}
	return environment, secrets, nil
}

// Run owns a service tree and exposes a private local channel until explicit shutdown.
func Run(ctx context.Context, recordPath string) (runErr error) {
	record, err := ReadRecord(recordPath)
	if err != nil {
		return err
	}
	lock, err := lockHost(filepath.Join(filepath.Dir(recordPath), "host.lock"))
	if err != nil {
		return fmt.Errorf("application host is already owned: %w", err)
	}
	defer lock.Close()
	lease, err := execenv.UseSharedDirectory(ctx, record.SourceRoot)
	if err != nil {
		return fmt.Errorf("protect application source directory: %w", err)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		runErr = errors.Join(runErr, lease.Finish(releaseCtx, nil))
	}()
	environment, secrets, err := Environment(record.Command.Config)
	if err != nil {
		return err
	}
	connections, closeConnections, err := startConnections(ctx, record.Command.Connections)
	if err != nil {
		return err
	}
	defer closeConnections()
	for variable, address := range connections {
		environment = append(environment, variable+"="+address)
		secrets = append(secrets, address)
	}
	for _, binding := range record.Command.Connections {
		if binding.Grant != "" {
			secrets = append(secrets, binding.Grant)
		}
	}
	log, err := openLog(filepath.Join(filepath.Dir(recordPath), "service.log"), record.HostID, 4<<20, secrets)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := log.close()
		stored, readErr := ReadRecord(recordPath)
		if readErr != nil {
			runErr = errors.Join(runErr, closeErr, readErr)
			return
		}
		if stored.HostID != record.HostID {
			runErr = errors.Join(runErr, closeErr)
			return
		}
		stored.LogRotation = log.rotation
		runErr = errors.Join(runErr, closeErr, WriteRecord(recordPath, stored))
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	record.Address = "http://" + listener.Addr().String()
	if err = WriteRecord(recordPath, record); err != nil {
		return err
	}
	serviceCtx, cancelService := context.WithCancel(ctx)
	defer cancelService()
	h := &host{record: record, path: recordPath, log: log, environment: environment, secrets: secrets, cancelService: cancelService, serviceDone: make(chan struct{}), shutdown: make(chan struct{})}
	server := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	go func() { defer close(h.serviceDone); h.supervise(serviceCtx) }()
	select {
	case <-ctx.Done():
	case <-h.shutdown:
	case err = <-serverDone:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	cancelService()
	<-h.serviceDone
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	return errors.Join(err, shutdownErr)
}

func (h *host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(credential) != len(h.record.Token) || subtle.ConstantTimeCompare([]byte(credential), []byte(h.record.Token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/status":
		h.mu.Lock()
		status := Status{HostID: h.record.HostID, InstanceID: h.record.Command.InstanceID, WorkspaceID: h.record.Command.WorkspaceID, RuntimeID: h.record.Command.RuntimeID, Observation: h.record.Observation}
		h.mu.Unlock()
		if err := json.NewEncoder(w).Encode(status); err != nil {
			return
		}
	case r.Method == http.MethodGet && r.URL.Path == "/logs":
		limit := 65536
		if value := r.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				http.Error(w, "invalid log limit", http.StatusBadRequest)
				return
			}
			limit = parsed
		}
		page, err := h.log.page(r.URL.Query().Get("cursor"), limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err = json.NewEncoder(w).Encode(page); err != nil {
			return
		}
	case r.Method == http.MethodPost && r.URL.Path == "/resume":
		var input struct {
			ExpectedGeneration int64 `json:"expected_generation"`
			Generation         int64 `json:"generation"`
			Revision           int64 `json:"revision"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid resume request", http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		if input.Generation < h.record.Command.Generation || input.Revision != h.record.Command.Revision || h.record.Observation.ProcessState != "running" || (h.record.Command.Generation != input.ExpectedGeneration && h.record.Command.Generation != input.Generation) {
			h.mu.Unlock()
			http.Error(w, "application resume ownership changed", http.StatusConflict)
			return
		}
		h.record.Command.Generation = input.Generation
		h.record.Observation.Generation = input.Generation
		if err := WriteRecord(h.path, h.record); err != nil {
			h.mu.Unlock()
			http.Error(w, "application resume could not be persisted", http.StatusInternalServerError)
			return
		}
		status := Status{HostID: h.record.HostID, InstanceID: h.record.Command.InstanceID, WorkspaceID: h.record.Command.WorkspaceID, RuntimeID: h.record.Command.RuntimeID, Observation: h.record.Observation}
		h.mu.Unlock()
		if err := json.NewEncoder(w).Encode(status); err != nil {
			return
		}
	case r.Method == http.MethodPost && r.URL.Path == "/stop":
		var input struct {
			Generation int64 `json:"generation"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid stop request", http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		if input.Generation < h.record.Command.Generation {
			h.mu.Unlock()
			http.Error(w, "stale application generation", http.StatusConflict)
			return
		}
		h.record.Command.Generation = input.Generation
		h.record.Observation.Generation = input.Generation
		h.mu.Unlock()
		h.cancelService()
		select {
		case <-h.serviceDone:
		case <-r.Context().Done():
			return
		}
		h.mu.Lock()
		cleanupFailed := h.cleanupFailed
		h.mu.Unlock()
		if cleanupFailed {
			http.Error(w, "application process cleanup is unconfirmed", http.StatusConflict)
			return
		}
		h.observation("stopped", "unknown", "")
		h.mu.Lock()
		status := Status{HostID: h.record.HostID, InstanceID: h.record.Command.InstanceID, WorkspaceID: h.record.Command.WorkspaceID, RuntimeID: h.record.Command.RuntimeID, Observation: h.record.Observation}
		h.mu.Unlock()
		if err := json.NewEncoder(w).Encode(status); err != nil {
			return
		}
		h.shutdownOnce.Do(func() { close(h.shutdown) })
	default:
		http.NotFound(w, r)
	}
}

func (h *host) observation(process, health, message string) {
	for _, secret := range h.secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	if len(message) > 2048 {
		message = message[:2048]
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.record.Observation.ProcessState = process
	h.record.Observation.HealthState = health
	h.record.Observation.Error = message
	h.log.mu.Lock()
	h.record.LogRotation = h.log.rotation
	h.log.mu.Unlock()
	if process == "starting" {
		started := time.Now().UTC().Format(time.RFC3339Nano)
		h.record.Observation.StartedAt = &started
	}
	if err := WriteRecord(h.path, h.record); err != nil {
		h.record.Observation.ProcessState = "unknown"
		h.record.Observation.Error = "application host state could not be persisted"
		h.cancelService()
	}
}

func (h *host) command(args []string) *exec.Cmd {
	program := args[0]
	if !filepath.IsAbs(program) && strings.ContainsAny(program, "/\\") {
		program = filepath.Join(h.record.WorkDir, program)
	}
	cmd := exec.Command(program, args[1:]...)
	cmd.Dir = h.record.WorkDir
	cmd.Env = h.environment
	cmd.Stdout = h.log
	cmd.Stderr = h.log
	return cmd
}

func (h *host) supervise(ctx context.Context) {
	for number, step := range h.record.Command.Config.Prepare {
		prepareCtx, cancel := context.WithTimeout(ctx, time.Duration(step.TimeoutSeconds)*time.Second)
		err := processtree.Run(prepareCtx, h.command(step.Args), time.Second)
		cancel()
		if err != nil {
			if errors.Is(err, processtree.ErrCleanup) {
				h.mu.Lock()
				h.cleanupFailed = true
				h.mu.Unlock()
				h.observation("unknown", "unknown", "preparation process cleanup is unconfirmed")
				return
			}
			if ctx.Err() != nil {
				h.observation("stopped", "unknown", "")
				return
			}
			h.observation("failed", "unknown", fmt.Sprintf("preparation step %d failed: %v", number+1, err))
			return
		}
	}
	config := h.record.Command.Config
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			h.observation("stopped", "unknown", "")
			return
		}
		if config.Port > 0 {
			listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(config.Port)))
			if err != nil {
				h.observation("failed", "unknown", "application port is already occupied")
				return
			}
			if err = listener.Close(); err != nil {
				h.observation("failed", "unknown", "application port reservation could not be released")
				return
			}
		}
		processCtx, cancel := context.WithCancel(ctx)
		started := make(chan struct{})
		exited := make(chan error, 1)
		go func() {
			exited <- processtree.RunWithStart(processCtx, h.command(config.Command), time.Second, func() error { h.observation("starting", "checking", ""); close(started); return nil })
		}()
		var exitErr error
		select {
		case <-started:
			exitErr = h.monitor(processCtx, exited)
		case exitErr = <-exited:
		case <-ctx.Done():
			exitErr = <-exited
		}
		cancel()
		if errors.Is(exitErr, processtree.ErrCleanup) {
			h.mu.Lock()
			h.cleanupFailed = true
			h.mu.Unlock()
			h.observation("unknown", "unknown", "application process cleanup is unconfirmed")
			return
		}
		if ctx.Err() != nil {
			h.observation("stopped", "unknown", "")
			return
		}
		message := "application process exited"
		if exitErr != nil {
			message = exitErr.Error()
		}
		h.observation("failed", "unknown", message)
		if !config.Restart.Enabled || attempt >= config.Restart.MaxAttempts {
			return
		}
		delay := time.Duration(min(300, config.Restart.DelaySeconds*(1<<min(attempt, 6)))) * time.Second
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			h.observation("stopped", "unknown", "")
			return
		case <-timer.C:
		}
	}
}

func (h *host) monitor(ctx context.Context, exited <-chan error) error {
	config := h.record.Command.Config
	interval := time.Duration(config.Health.IntervalSeconds) * time.Second
	if config.Health.Kind == "none" {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(min(interval, 250*time.Millisecond))
	defer ticker.Stop()
	ready := false
	deadline := time.Now().Add(time.Duration(max(1, config.Health.TimeoutSeconds)) * time.Second)
	for {
		select {
		case err := <-exited:
			return err
		case <-ctx.Done():
			return <-exited
		case <-ticker.C:
			checkErr := CheckHealth(ctx, config)
			if checkErr == nil {
				health := "healthy"
				if config.Health.Kind == "none" {
					health = "none"
				}
				h.observation("running", health, "")
				if !ready {
					ready = true
					ticker.Reset(interval)
				}
			} else if ready || time.Now().After(deadline) {
				h.observation("running", "unhealthy", checkErr.Error())
			}
		}
	}
}

// CheckHealth probes only the explicitly configured loopback service and never follows redirects.
func CheckHealth(ctx context.Context, config protocol.ApplicationConfig) error {
	if config.Health.Kind == "none" {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(config.Port))
	if config.Health.Kind == "tcp" {
		connection, err := (&net.Dialer{}).DialContext(checkCtx, "tcp", address)
		if err != nil {
			return errors.New("application port is not ready")
		}
		return connection.Close()
	}
	request, err := http.NewRequestWithContext(checkCtx, http.MethodGet, "http://"+address+config.Health.Path, nil)
	if err != nil {
		return err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("application health endpoint is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("application health endpoint returned %d", response.StatusCode)
	}
	return nil
}
