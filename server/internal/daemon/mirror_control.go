package daemon

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
)

type mirrorControlGeneration uint64

func (d *Daemon) beginMirrorControlConnection(parent context.Context) (mirrorControlGeneration, context.Context, context.CancelFunc) {
	d.mu.Lock()
	cancelPrevious := d.mirrorControlCancel
	existingMirrors := make([]*mirror.RuntimeMirror, 0, len(d.runtimeMirrors))
	for _, runtimeMirror := range d.runtimeMirrors {
		existingMirrors = append(existingMirrors, runtimeMirror)
	}
	d.mirrorControlGeneration++
	generation := d.mirrorControlGeneration
	ctx, cancel := context.WithCancel(parent)
	d.mirrorControlCancel = cancel
	d.mu.Unlock()
	if cancelPrevious != nil {
		cancelPrevious()
	}
	closeCtx, closeCancel := context.WithTimeout(parent, 5*time.Second)
	defer closeCancel()
	for _, runtimeMirror := range existingMirrors {
		if err := runtimeMirror.CloseUnattachedPeers(closeCtx); err != nil {
			d.logger.Debug("close unattached mirror peers failed", "error", err)
		}
	}
	return generation, ctx, cancel
}

func (d *Daemon) mirrorControlGenerationIsCurrent(generation mirrorControlGeneration) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.mirrorControlGeneration == generation
}

func (d *Daemon) replayActiveMirrorViewerStates(enqueue func([]byte) (*wsOutbound, error), generation mirrorControlGeneration) {
	if enqueue == nil {
		return
	}
	for _, tracked := range d.trackedRuntimeMirrors() {
		runtimeID := tracked.runtimeID
		workspaceID := tracked.workspaceID
		tracked.mirror.SetViewerStateHook(func(change mirror.ViewerStateChange) {
			if _, err := d.sendMirrorViewerState(enqueue, workspaceID, runtimeID, change.ViewerID, change.Active); err != nil {
				d.logger.Debug("mirror viewer state dropped", "runtime_id", runtimeID, "error", err)
			}
		}, uint64(generation))
	}
}

func (d *Daemon) runtimeMirror(runtimeID string) (*mirror.RuntimeMirror, bool) {
	d.mu.Lock()
	generation := d.mirrorControlGeneration
	d.mu.Unlock()
	runtimeMirror, _, ok := d.runtimeMirrorForOffer(runtimeID, generation)
	return runtimeMirror, ok
}

func (d *Daemon) runtimeMirrorForOffer(runtimeID string, generation mirrorControlGeneration) (*mirror.RuntimeMirror, bool, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if generation == 0 || d.mirrorControlGeneration != generation {
		return nil, false, false
	}
	if _, tracked := d.runtimeIndex[runtimeID]; !tracked {
		return nil, false, false
	}
	if runtimeMirror, ok := d.runtimeMirrors[runtimeID]; ok {
		return runtimeMirror, false, true
	}
	runtimeMirror := mirror.NewRuntimeMirror(mirror.NativeCapturer{}, 500*time.Millisecond)
	runtimeMirror.SetCaptureFailureHandler(func(err error) {
		d.logger.Warn("runtime mirror capture unavailable",
			"runtime_id", runtimeID,
			"reason", mirror.ControlReasonFromError(err),
			"error", err,
		)
	})
	d.runtimeMirrors[runtimeID] = runtimeMirror
	return runtimeMirror, true, true
}
