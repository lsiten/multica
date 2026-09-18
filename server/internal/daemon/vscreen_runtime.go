package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// VscreenInputHandler supplies actual checked input and a native quiescence barrier.
// Implementations must never fall back to global keyboard/mouse dispatch.
type VscreenInputHandler interface {
	Act(context.Context, vscreen.Action) (vscreen.ActionResult, error)
	Quiesce(context.Context, vscreen.ResourceKey) error
	Dispose(context.Context, vscreen.ResourceKey) error
}

// VscreenTakeoverHandler coordinates intervention persistence and provider cancellation.
type VscreenTakeoverHandler func(context.Context, protocol.VscreenCommand, *vscreen.Actor) error

type vscreenRuntime struct {
	interventions     vscreenInterventions
	commandMu         sync.Mutex
	commands          map[string]vscreenCachedCommand
	grantClosed       bool
	grantBlockedUntil time.Time
	grantWG           sync.WaitGroup
	runCancel         context.CancelFunc
	runDone           chan struct{}
	grantMu           sync.Mutex
	grants            map[vscreenGrantKey]vscreenGrantEntry
	mu                sync.Mutex
	client            *hostclient.Client
	manager           *vscreen.Manager
	driver            *vscreenNativeDriver
	hub               *mirror.CaptureHub
	enabled           map[protocol.ResourceKey]bool
	revisions         map[string]uint64
	closed            bool
}

// SetVscreenInputHandler installs a local trusted input implementation before GUI work.
func (d *Daemon) SetVscreenInputHandler(handler VscreenInputHandler) {
	d.vscreenMu.Lock()
	defer d.vscreenMu.Unlock()
	d.vscreenInput = handler
	if d.vscreen != nil {
		d.vscreen.driver.setInput(handler)
	}
}

// SetVscreenTakeoverHandler installs the intervention coordinator; absence fails closed.
func (d *Daemon) SetVscreenTakeoverHandler(handler VscreenTakeoverHandler) {
	d.vscreenMu.Lock()
	defer d.vscreenMu.Unlock()
	d.vscreenTakeover = handler
}

func (d *Daemon) vscreenResource(workspaceID, runtimeID string) (protocol.ResourceKey, error) {
	d.mu.Lock()
	ws := d.workspaces[workspaceID]
	_, tracked := d.runtimeIndex[runtimeID]
	belongs := false
	if ws != nil {
		for _, id := range ws.runtimeIDs {
			if id == runtimeID {
				belongs = true
				break
			}
		}
	}
	d.mu.Unlock()
	if !tracked || !belongs {
		return protocol.ResourceKey{}, &vscreen.Error{Reason: protocol.VscreenSourceGone}
	}
	u, err := url.Parse(d.cfg.ServerBaseURL)
	if err != nil {
		return protocol.ResourceKey{}, err
	}
	u.Host = strings.ToLower(u.Host)
	if u.Scheme == "https" {
		u.Host = strings.TrimSuffix(u.Host, ":443")
	}
	if u.Scheme == "http" {
		u.Host = strings.TrimSuffix(u.Host, ":80")
	}
	u.Path = strings.TrimSuffix(path.Clean(u.Path), "/")
	if u.Path == "." {
		u.Path = ""
	}
	key := protocol.ResourceKey{BackendIdentity: u.String(), WorkspaceID: workspaceID, RuntimeID: runtimeID, UID: uint32(os.Getuid())}
	return key, key.Validate()
}

func (d *Daemon) vscreenRuntime() *vscreenRuntime {
	d.vscreenMu.Lock()
	defer d.vscreenMu.Unlock()
	if d.vscreen == nil {
		driver := &vscreenNativeDriver{displays: make(map[protocol.ResourceKey]vscreen.Display), input: d.vscreenInput}
		d.vscreen = &vscreenRuntime{commands: make(map[string]vscreenCachedCommand), grants: make(map[vscreenGrantKey]vscreenGrantEntry), driver: driver, manager: vscreen.NewManager(driver, vscreen.SystemClock{}), enabled: make(map[protocol.ResourceKey]bool), revisions: make(map[string]uint64)}
		d.loadVscreenInterventions(d.vscreen)
		if d.cfg.NativeVscreenPreferencesPath != "" {
			raw, err := os.ReadFile(d.cfg.NativeVscreenPreferencesPath)
			if err == nil && len(raw) <= 65536 {
				var keys []protocol.ResourceKey
				if json.Unmarshal(raw, &keys) == nil {
					for _, key := range keys {
						if key.Validate() == nil {
							d.vscreen.enabled[key] = true
						}
					}
				}
			}
		}

	}
	return d.vscreen
}

func (d *Daemon) startVscreenHost(ctx context.Context, s *vscreenRuntime) error {
	if s.closed {
		return hostclient.ErrClosed
	}
	if s.client != nil {
		return nil
	}
	if d.cfg.NativeHostExecutable == "" || d.cfg.NativeHostBuild == "" {
		return native.ErrUnavailable
	}
	client, err := hostclient.Start(ctx, hostclient.Config{Executable: d.cfg.NativeHostExecutable, Build: d.cfg.NativeHostBuild, Media: true, AppControl: true})
	if err != nil {
		return err
	}
	s.client = client
	s.driver.setClient(client)
	if d.vscreenInput == nil {
		s.driver.setInput(&vscreenAppInput{client: client})
	}
	s.hub = mirror.NewCaptureHub(VscreenCaptureProvider{Client: client})
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.runCancel = cancel
	s.runDone = make(chan struct{})
	go func() {
		defer close(s.runDone)
		if err := s.manager.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
			d.logger.Warn("virtual screen authority stopped")
		}
	}()
	return nil
}

// VscreenActor returns the runtime-owned actor; obtaining it does not create a display.
func (d *Daemon) VscreenActor(workspaceID, runtimeID string) (*vscreen.Actor, error) {
	key, err := d.vscreenResource(workspaceID, runtimeID)
	if err != nil {
		return nil, err
	}
	return d.vscreenRuntime().manager.For(key)
}

func (d *Daemon) closeVscreenRuntime(runtimeID string) {
	d.vscreenMu.Lock()
	reporter := d.vscreenReporter
	s := d.vscreen
	d.vscreenMu.Unlock()
	if reporter != nil {
		if err := reporter.CancelScope("", runtimeID); err != nil {
			d.logger.Warn("virtual screen report scope cleanup failed")
		}
	}
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for key := range s.enabled {
		if key.RuntimeID == runtimeID {
			a, err := s.manager.For(key)
			if err == nil {
				err = a.Dispose(ctx)
			}
			if err != nil {
				d.logger.Warn("virtual screen cleanup failed", "runtime_id", runtimeID)
			}
			delete(s.enabled, key)
		}
	}
}

func (d *Daemon) closeVscreens() {
	d.vscreenMu.Lock()
	s := d.vscreen
	d.vscreenMu.Unlock()
	if s == nil {
		return
	}
	s.grantMu.Lock()
	s.grantClosed = true
	for _, entry := range s.grants {
		if entry.cancel != nil {
			entry.cancel()
		}
	}
	s.grantMu.Unlock()
	s.grantWG.Wait()
	d.closeRuntimeMirrors()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.runCancel != nil {
		s.runCancel()
		<-s.runDone
	}
	err := s.manager.Close(ctx)
	if s.client != nil {
		err = errors.Join(err, s.client.Close())
	}
	if err != nil {
		d.logger.Warn("virtual screen shutdown incomplete")
	}
}

func managedVscreenCapabilities() []string {
	if !native.Supported() {
		return nil
	}
	return []string{protocol.DaemonCapabilityVirtualScreenV1, protocol.DaemonCapabilityScreenMirrorVideoV2, protocol.DaemonCapabilityMirrorViewerGrantV1}
}

func (d *Daemon) suspendVscreens(g mirrorControlGeneration) {
	if !d.mirrorControlGenerationIsCurrent(g) {
		return
	}
	d.vscreenMu.Lock()
	s := d.vscreen
	d.vscreenMu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	keys := make([]protocol.ResourceKey, 0, len(s.enabled))
	for k := range s.enabled {
		keys = append(keys, k)
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, key := range keys {
		if a, err := s.manager.For(key); err == nil {
			if err = a.Suspend(ctx); err != nil {
				d.logger.Warn("virtual screen suspension incomplete")
			}
		}
	}
}

func (d *Daemon) pruneVscreens() {
	d.vscreenMu.Lock()
	s := d.vscreen
	d.vscreenMu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	keys := make([]protocol.ResourceKey, 0, len(s.enabled))
	for key := range s.enabled {
		keys = append(keys, key)
	}
	s.mu.Unlock()
	for _, key := range keys {
		if _, err := d.vscreenResource(key.WorkspaceID, key.RuntimeID); err != nil {
			d.closeVscreenRuntime(key.RuntimeID)
		}
	}
}
