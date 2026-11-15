package daemon

import (
	"context"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/mirror/nativecapture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type vscreenRuntime struct {
	grantClosed       bool
	grantBlockedUntil time.Time
	grantWG           sync.WaitGroup
	grantMu           sync.Mutex
	grants            map[vscreenGrantKey]vscreenGrantEntry
	mu                sync.Mutex
	client            *nativecapture.Client
	hub               *mirror.CaptureHub
	closed            bool
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
		return protocol.ResourceKey{}, nativecapture.ErrUnavailable
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
	if d.mirrorProcessMode() {
		return nil
	}
	d.vscreenMu.Lock()
	defer d.vscreenMu.Unlock()
	if d.vscreen == nil {
		d.vscreen = &vscreenRuntime{grants: make(map[vscreenGrantKey]vscreenGrantEntry)}
	}
	return d.vscreen
}

func (d *Daemon) startVscreenHost(ctx context.Context, s *vscreenRuntime) error {
	if s.closed {
		return nativecapture.ErrClosed
	}
	if s.client != nil {
		return nil
	}
	if d.cfg.NativeHostExecutable == "" || d.cfg.NativeHostBuild == "" {
		return nativecapture.ErrUnavailable
	}
	client, err := nativecapture.Start(ctx, nativecapture.Config{Executable: d.cfg.NativeHostExecutable, Build: d.cfg.NativeHostBuild})
	if err != nil {
		return err
	}
	if d.mirrorChild != nil && !d.mirrorChild.watchNative(client) {
		client.Close()
		return nativecapture.ErrClosed
	}
	s.client = client
	s.hub = mirror.NewCaptureHub(MirrorCaptureProvider{Client: client})
	return nil
}

func (d *Daemon) closeVscreenRuntime(runtimeID string) {
	if d.mirrorProcessMode() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := d.mirrorCall(ctx, mirrorProcessRequest{Operation: "remove", RuntimeID: runtimeID}, nil); err != nil && d.logger != nil {
			d.logger.Warn("mirror runtime removal unconfirmed", "runtime_id", runtimeID, "error", err)
		}
		return
	}
	d.vscreenMu.Lock()
	s := d.vscreen
	d.vscreenMu.Unlock()
	if s == nil {
		return
	}
	d.removeVscreenRuntime(s, runtimeID)
}

func (d *Daemon) closeVscreens() {
	if d.mirrorProcessMode() {
		d.closeMirrorProcess()
		return
	}
	if err := d.closeVscreensResult(); err != nil {
		d.logger.Warn("virtual screen shutdown incomplete")
	}
}
func (d *Daemon) closeVscreensResult() error {
	d.vscreenMu.Lock()
	s := d.vscreen
	d.vscreenMu.Unlock()
	if s == nil {
		return nil
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
	var err error
	if s.client != nil {
		err = s.client.Close()
	}
	return err
}

func (d *Daemon) removeVscreenRuntime(s *vscreenRuntime, runtimeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d.mu.Lock()
	runtimeMirror := d.runtimeMirrors[runtimeID]
	delete(d.runtimeMirrors, runtimeID)
	d.mu.Unlock()
	if runtimeMirror != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runtimeMirror.CloseUnattachedPeers(ctx); err != nil && d.logger != nil {
			d.logger.Debug("close removed runtime mirror peers", "runtime_id", runtimeID, "error", err)
		}
	}
}

// managedMirrorCapabilities advertises the read-only video feed and its grant.
func managedMirrorCapabilities() []string {
	if !nativecapture.Supported() {
		return nil
	}
	return []string{protocol.DaemonCapabilityScreenMirrorVideoV2, protocol.DaemonCapabilityMirrorViewerGrantV1}
}

func (d *Daemon) suspendVscreens(g mirrorConnectionGeneration) {
	if d.mirrorProcessMode() {
		if client, err := d.currentMirrorProcess(); err == nil {
			client.unbind(uint64(g))
		}
		return
	}
	if !d.mirrorConnectionCurrent(g) {
		return
	}
	d.vscreenMu.Lock()
	s := d.vscreen
	d.vscreenMu.Unlock()
	if s == nil {
		return
	}
	d.closeRuntimeMirrors()
}

func (d *Daemon) pruneVscreens() {
	if d.mirrorProcessMode() {
		d.refreshMirrorRoster()
		return
	}
	d.vscreenMu.Lock()
	s := d.vscreen
	d.vscreenMu.Unlock()
	if s == nil {
		return
	}
	d.closeMissingRuntimeMirrors()
}

func (d *Daemon) closeMissingRuntimeMirrors() {
	d.mu.Lock()
	detached := make([]*mirror.RuntimeMirror, 0)
	for runtimeID, runtimeMirror := range d.runtimeMirrors {
		if _, tracked := d.runtimeIndex[runtimeID]; tracked {
			continue
		}
		delete(d.runtimeMirrors, runtimeID)
		detached = append(detached, runtimeMirror)
	}
	d.mu.Unlock()
	d.closeDetachedRuntimeMirrors(detached)
}
