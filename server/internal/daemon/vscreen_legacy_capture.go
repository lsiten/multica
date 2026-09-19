package daemon

import (
	"context"
	"image"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
)

// vscreenPrimaryCapturer pins JPEG v1 to the catalog primary incarnation. It
// never prompts and discards frames if the source changes during capture.
type vscreenPrimaryCapturer struct {
	daemon *Daemon
	source native.SourceDescriptor
}

func (c vscreenPrimaryCapturer) Capture(ctx context.Context) (image.Image, error) {
	validate := func() error {
		sources, err := c.daemon.vscreenSources(ctx, c.source.Resource.WorkspaceID, c.source.Resource.RuntimeID)
		if err != nil {
			return vscreenCaptureError(err)
		}
		for _, source := range sources {
			if source == c.source && source.Primary {
				return nil
			}
		}
		return mirror.ErrNoDisplay
	}
	if err := validate(); err != nil {
		return nil, err
	}
	bounds := image.Rect(int(c.source.X), int(c.source.Y), int(c.source.X)+int(c.source.LogicalWidth), int(c.source.Y)+int(c.source.LogicalHeight))
	frame, err := (mirror.NativeCapturer{NoPermissionPrompt: true, Bounds: &bounds}).Capture(ctx)
	if err != nil {
		return nil, err
	}
	if err = validate(); err != nil {
		return nil, err
	}
	return frame, nil
}

func (d *Daemon) managedRuntimeMirror(runtimeID string, g mirrorControlGeneration, sources []native.SourceDescriptor) (*mirror.RuntimeMirror, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if g == 0 || d.mirrorControlGeneration != g {
		return nil, false
	}
	if _, ok := d.runtimeIndex[runtimeID]; !ok {
		return nil, false
	}
	if rm := d.runtimeMirrors[runtimeID]; rm != nil {
		return rm, true
	}
	var capturer mirror.Capturer = unavailableVscreenCapturer{}
	for _, source := range sources {
		if source.Primary && source.Source.Kind != "virtual" {
			capturer = vscreenPrimaryCapturer{daemon: d, source: source}
			break
		}
	}
	rm := mirror.NewRuntimeMirror(capturer, 500*time.Millisecond)
	rm.SetCaptureFailureHandler(func(err error) {
		d.logger.Debug("managed capture unavailable", "reason", mirror.ControlReasonFromError(err))
	})
	d.wireMirrorControl(rm)
	d.runtimeMirrors[runtimeID] = rm
	return rm, true
}

type unavailableVscreenCapturer struct{}

func (unavailableVscreenCapturer) Capture(context.Context) (image.Image, error) {
	return nil, mirror.ErrNoDisplay
}
