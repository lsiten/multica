package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type applicationProcessBinding struct {
	WorkspacesRoot string                                     `json:"workspaces_root"`
	SourceAddress  string                                     `json:"source_address"`
	Grants         []protocol.ApplicationServiceGrantResponse `json:"grants"`
}
type applicationProcessInventory struct {
	InstanceID   string                            `json:"instance_id"`
	PID          int                               `json:"pid"`
	Runtimes     int                               `json:"runtimes"`
	Observations []protocol.ApplicationObservation `json:"observations"`
	Unknown      bool                              `json:"unknown"`
}
type applicationRuntimeCohort struct {
	client *ApplicationServiceClient
	cancel context.CancelFunc
	done   chan struct{}
}
type applicationProcessService struct {
	bootstrap     runtimeproc.Bootstrap
	daemon        *Daemon
	mu            sync.RWMutex
	cohorts       map[string]*applicationRuntimeCohort
	initialized   bool
	sourceAddress string
	lifetime      context.Context
	cancel        context.CancelFunc
	done          chan struct{}
}

// RunApplicationService owns application management and direct data tunnels.
// It does not run account discovery, task execution, or repository-cache loops.
func RunApplicationService(ctx context.Context, b runtimeproc.Bootstrap) error {
	if b.Identity.Scope.Service != "application" {
		return errors.New("unsupported application role")
	}
	if err := runtimeproc.PrepareRoot(b.Root); err != nil {
		return err
	}
	lock, err := lockMirrorProcessDomain(filepath.Join(b.Root, "application-manager.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	life, cancel := context.WithCancel(ctx)
	defer cancel()
	d := &Daemon{cfg: Config{ServerBaseURL: b.Identity.Scope.Backend, DaemonID: b.Identity.Scope.DaemonID, Profile: b.Identity.Scope.Profile}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workspaces: map[string]*workspaceState{}, runtimeIndex: map[string]Runtime{}, applicationWake: make(chan struct{}, 1)}
	s := &applicationProcessService{bootstrap: b, daemon: d, cohorts: map[string]*applicationRuntimeCohort{}, lifetime: life, cancel: cancel, done: make(chan struct{})}
	d.applicationTransport = s.transport
	d.applicationSourceProvider = s.prepareSource
	service, err := runtimeproc.NewService(runtimeproc.Config{Bootstrap: b, Capabilities: []string{"application.bind", "application.remove", "application.wake"}, ReadCapabilities: []string{"application.inventory"}, Handler: s.mutate, ReadHandler: s.read, Ready: s.ready, Shutdown: s.shutdown})
	if err != nil {
		return err
	}
	return service.Serve(ctx)
}
func (s *applicationProcessService) ready(ctx context.Context) error {
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-s.lifetime.Done():
				return
			case <-ticker.C:
				s.mu.RLock()
				initialized := s.initialized
				s.mu.RUnlock()
				if initialized {
					s.daemon.gcApplicationStorage(s.lifetime, time.Now())
				}
			}
		}
	}()
	return nil
}
func (s *applicationProcessService) transport(id string) applicationTransport {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c := s.cohorts[id]; c != nil {
		return c.client
	}
	return unavailableApplicationTransport{}
}
func (s *applicationProcessService) mutate(ctx context.Context, r runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	switch r.Operation {
	case "application.bind":
		var binding applicationProcessBinding
		if json.Unmarshal(r.Payload, &binding) != nil {
			return nil, &runtimeproc.Error{Code: "malformed", Message: "invalid application binding"}
		}
		if err := s.bind(ctx, binding); err != nil {
			return nil, &runtimeproc.Error{Code: "application_bind", Message: err.Error()}
		}
	case "application.remove":
		var input struct {
			RuntimeID string `json:"runtime_id"`
		}
		if json.Unmarshal(r.Payload, &input) != nil || input.RuntimeID == "" {
			return nil, &runtimeproc.Error{Code: "malformed", Message: "missing runtime"}
		}
		if err := s.remove(ctx, input.RuntimeID, true); err != nil {
			return nil, &runtimeproc.Error{Code: "application_remove", Message: err.Error()}
		}
	case "application.wake":
		s.daemon.wakeApplications()
	default:
		return nil, &runtimeproc.Error{Code: "unknown_operation", Message: "unknown application operation"}
	}
	return json.RawMessage(`{}`), nil
}
func (s *applicationProcessService) bind(ctx context.Context, b applicationProcessBinding) error {
	canonical, err := filepath.EvalSymlinks(b.WorkspacesRoot)
	if err != nil || !filepath.IsAbs(b.WorkspacesRoot) || canonical != filepath.Clean(b.WorkspacesRoot) {
		return errors.New("application workspaces root is not canonical")
	}
	if err = validateApplicationCallbackAddress(b.SourceAddress); err != nil {
		return err
	}
	if len(b.Grants) > 256 {
		return errors.New("too many application runtimes")
	}
	clients := map[string]*ApplicationServiceClient{}
	for _, grant := range b.Grants {
		client, err := NewApplicationServiceClient(s.bootstrap.Identity.Scope.Backend, s.bootstrap.Identity.Scope.DaemonID, s.bootstrap.Identity.InstanceID, grant)
		if err != nil {
			return err
		}
		if _, exists := clients[grant.RuntimeID]; exists {
			return errors.New("duplicate application runtime")
		}
		clients[grant.RuntimeID] = client
	}
	s.mu.Lock()
	if s.initialized && (s.daemon.cfg.WorkspacesRoot != canonical || s.sourceAddress != b.SourceAddress) {
		s.mu.Unlock()
		return errors.New("application binding root changed")
	}
	if !s.initialized {
		s.daemon.cfg.WorkspacesRoot = canonical
		s.sourceAddress = b.SourceAddress
		s.initialized = true
	}
	previous := map[string]*applicationRuntimeCohort{}
	for id, c := range s.cohorts {
		previous[id] = c
	}
	s.mu.Unlock()
	for id, c := range previous {
		if next := clients[id]; next != nil {
			if next.grant.WorkspaceID != c.client.grant.WorkspaceID || next.grant.Generation < c.client.grant.Generation || next.grant.Generation == c.client.grant.Generation && next.grant.Token != c.client.grant.Token {
				return errors.New("stale application grant generation or scope")
			}
		}
	}
	for id, c := range previous {
		next := clients[id]
		if next != nil && next.grant.Generation == c.client.grant.Generation && next.grant.Token == c.client.grant.Token {
			delete(clients, id)
			continue
		}
		if err = s.remove(ctx, id, next == nil); err != nil {
			return err
		}
	}
	for id, client := range clients {
		if err = ctx.Err(); err != nil {
			return err
		}
		life, cancel := context.WithCancel(s.lifetime)
		c := &applicationRuntimeCohort{client: client, cancel: cancel, done: make(chan struct{})}
		s.daemon.mu.Lock()
		workspace := s.daemon.workspaces[client.grant.WorkspaceID]
		if workspace == nil {
			workspace = &workspaceState{}
			s.daemon.workspaces[client.grant.WorkspaceID] = workspace
		}
		workspace.runtimeIDs = append(workspace.runtimeIDs, id)
		s.daemon.runtimeIndex[id] = Runtime{}
		s.daemon.mu.Unlock()
		s.mu.Lock()
		s.cohorts[id] = c
		s.mu.Unlock()
		go s.runRuntime(life, id, c)
	}
	return nil
}
func (s *applicationProcessService) runRuntime(ctx context.Context, id string, c *applicationRuntimeCohort) {
	defer close(c.done)
	ctx, cancel := context.WithDeadline(ctx, c.client.grant.ExpiresAt)
	defer cancel()
	tunnelDone := make(chan struct{})
	go func() { defer close(tunnelDone); s.daemon.serveApplicationTunnel(ctx, id) }()
	defer func() { cancel(); <-tunnelDone }()
	var workers sync.WaitGroup
	defer workers.Wait()
	semaphore := make(chan struct{}, 4)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		s.daemon.pollRuntimeApplications(ctx, semaphore, &workers, id)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.daemon.applicationWake:
		}
	}
}
func (s *applicationProcessService) remove(ctx context.Context, id string, stop bool) error {
	s.mu.RLock()
	c := s.cohorts[id]
	s.mu.RUnlock()
	if c == nil {
		return nil
	}
	c.cancel()
	select {
	case <-c.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if stop {
		s.daemon.stopRuntimeApplications(ctx, id)
		records, err := s.daemon.applicationRecords()
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.Command.RuntimeID == id {
				path, _, err := s.daemon.applicationRecord(record.Command)
				if err != nil {
					return err
				}
				if err = applicationhost.WaitStopped(ctx, path, record.HostID); err != nil {
					return err
				}
				if err = c.client.Observe(ctx, record.Observation); err != nil && ctx.Err() == nil {
					s.daemon.logger.Debug("application stopped observation deferred", "instance_id", record.Command.InstanceID, "error", err)
				}
			}
		}
	}
	s.mu.Lock()
	delete(s.cohorts, id)
	s.mu.Unlock()
	c.client.CloseIdleConnections()
	s.daemon.mu.Lock()
	delete(s.daemon.runtimeIndex, id)
	for _, workspace := range s.daemon.workspaces {
		ids := workspace.runtimeIDs[:0]
		for _, current := range workspace.runtimeIDs {
			if current != id {
				ids = append(ids, current)
			}
		}
		workspace.runtimeIDs = ids
	}
	s.daemon.mu.Unlock()
	if stop {
		s.daemon.applicationRegistries.Delete(id)
		s.daemon.applicationCommands.Range(func(key, value any) bool {
			if value.(protocol.ApplicationControlCommand).RuntimeID == id {
				s.daemon.applicationCommands.Delete(key)
			}
			return true
		})
	}
	return nil
}
func (s *applicationProcessService) read(ctx context.Context, r runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	inventory := applicationProcessInventory{InstanceID: s.bootstrap.Identity.InstanceID, PID: os.Getpid(), Observations: []protocol.ApplicationObservation{}}
	s.mu.RLock()
	inventory.Runtimes = len(s.cohorts)
	initialized := s.initialized
	if !initialized {
		inventory.Unknown = true
	}
	for id, c := range s.cohorts {
		registry, ok := s.daemon.applicationRegistries.Load(id)
		if !ok || time.Now().After(c.client.grant.ExpiresAt) {
			inventory.Unknown = true
			continue
		}
		if time.Since(registry.(applicationRegistry).observedAt) > 3*time.Minute {
			inventory.Unknown = true
		}
	}
	s.mu.RUnlock()
	if initialized {
		commands := map[string]protocol.ApplicationControlCommand{}
		s.daemon.applicationCommands.Range(func(key, value any) bool {
			commands[key.(string)] = value.(protocol.ApplicationControlCommand)
			return true
		})
		records, err := s.daemon.applicationRecords()
		if err != nil {
			inventory.Unknown = true
		} else {
			for _, record := range records {
				if _, exists := commands[record.Command.InstanceID]; !exists {
					commands[record.Command.InstanceID] = record.Command
				}
			}
		}
		if len(commands) > 1024 {
			return nil, &runtimeproc.Error{Code: "resource_exhausted", Message: "application inventory exceeds limit"}
		}
		for _, command := range commands {
			observation, err := s.daemon.inspectApplication(ctx, command)
			if err != nil {
				inventory.Unknown = true
				observation = applicationObservation(command, "unknown", "unknown", err.Error())
			}
			inventory.Observations = append(inventory.Observations, observation)
		}
	}
	return marshalRaw(inventory), nil
}
func (s *applicationProcessService) shutdown(ctx context.Context) error {
	s.cancel()
	<-s.done
	s.mu.RLock()
	ids := make([]string, 0, len(s.cohorts))
	for id := range s.cohorts {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	var result error
	for _, id := range ids {
		result = errors.Join(result, s.remove(ctx, id, true))
	}
	return result
}
