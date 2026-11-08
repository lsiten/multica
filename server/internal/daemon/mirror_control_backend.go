package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/globalinput"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// sourceCatalogTTL bounds reuse of a cached native source catalog. Geometry
// changes invalidate through the message's geometry revision, so a short TTL
// is sufficient without querying the native host on every input event.
const sourceCatalogTTL = 3 * time.Second

// mirrorControlBackend implements mirror.ControlBackend for one daemon. It
// resolves granted sources to the shared arbitration resource, enforces the
// host-side human-interaction master switch (default off), and dispatches
// accepted input to the platform global injector.
type mirrorControlBackend struct {
	d *Daemon

	mu      sync.Mutex
	catalog map[string]cachedCatalog
	inputMu sync.Mutex
	held    map[string]heldNativeInput
}

type cachedCatalog struct {
	sources []native.SourceDescriptor
	at      time.Time
}

func newMirrorControlBackend(d *Daemon) *mirrorControlBackend {
	return &mirrorControlBackend{d: d, catalog: make(map[string]cachedCatalog), held: make(map[string]heldNativeInput)}
}

// SetHumanInteractionEnabled toggles the host master switch for remote human
// control. It never affects agent actions.
func (d *Daemon) SetHumanInteractionEnabled(enabled bool) {
	if d.mirrorProcessMode() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := d.mirrorCall(ctx, mirrorProcessRequest{Operation: "human_set", Payload: marshalRaw(map[string]bool{"enabled": enabled})}, nil); err != nil && d.logger != nil {
			d.logger.Warn("mirror interaction setting unavailable", "error", err)
		}
		return
	}
	d.humanInteractionEnabled.Store(enabled)
}

func (d *Daemon) HumanInteractionEnabled() bool {
	if d.mirrorProcessMode() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var value struct {
			Enabled bool `json:"enabled"`
		}
		return d.mirrorCall(ctx, mirrorProcessRequest{Operation: "human_get"}, &value) == nil && value.Enabled
	}
	return d.humanInteractionEnabled.Load()
}

func (b *mirrorControlBackend) InteractionEnabled() bool {
	return b.d.HumanInteractionEnabled()
}

func (b *mirrorControlBackend) catalogFor(runtimeID string) ([]native.SourceDescriptor, bool) {
	b.mu.Lock()
	cached, ok := b.catalog[runtimeID]
	b.mu.Unlock()
	if ok && time.Since(cached.at) < sourceCatalogTTL {
		return cached.sources, true
	}
	return nil, false
}

func (b *mirrorControlBackend) remember(runtimeID string, sources []native.SourceDescriptor) {
	b.mu.Lock()
	b.catalog[runtimeID] = cachedCatalog{sources: sources, at: time.Now()}
	b.mu.Unlock()
}

// ResourceForInput refreshes a mismatched catalog before arbitration. Native
// dispatch must not hold the arbiter lock while waiting for the host catalog.
func (b *mirrorControlBackend) ResourceForInput(grant protocol.MirrorControlGrant, msg protocol.MirrorInputMessage) (protocol.ResourceKey, bool) {
	if grant.WorkspaceID == "" || grant.RuntimeID == "" {
		return protocol.ResourceKey{}, false
	}
	if descriptor, ok := b.cachedInputSource(grant, msg); ok {
		return descriptor.Resource, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sources, err := b.d.vscreenSources(ctx, grant.WorkspaceID, grant.RuntimeID)
	if err != nil {
		return protocol.ResourceKey{}, false
	}
	b.remember(grant.RuntimeID, sources)
	descriptor, ok := b.cachedInputSource(grant, msg)
	return descriptor.Resource, ok
}

func (b *mirrorControlBackend) cachedInputSource(grant protocol.MirrorControlGrant, msg protocol.MirrorInputMessage) (native.SourceDescriptor, bool) {
	sources, ok := b.catalogFor(grant.RuntimeID)
	if !ok {
		return native.SourceDescriptor{}, false
	}
	for _, source := range sources {
		if source.Source == grant.Source && source.NativeEpoch == grant.NativeEpoch && source.Generation == grant.SourceGeneration && source.GeometryRevision == msg.GeometryRevision {
			return source, true
		}
	}
	return native.SourceDescriptor{}, false
}

// DispatchInput applies one accepted gesture. It returns "" on success or a
// protocol.MirrorInput* nack reason.
func (b *mirrorControlBackend) DispatchInput(_ context.Context, resource protocol.ResourceKey, grant protocol.MirrorControlGrant, msg protocol.MirrorInputMessage) string {
	descriptor, ok := b.cachedInputSource(grant, msg)
	if !ok || descriptor.Resource != resource {
		return protocol.MirrorInputStale
	}
	// A geometry change invalidates the frame the viewer clicked on.
	if descriptor.GeometryRevision != msg.GeometryRevision || descriptor.NativeEpoch != msg.NativeEpoch {
		return protocol.MirrorInputStale
	}

	// Agent-managed virtual screens are injected per-PID behind a task lease.
	// A bare human viewer holds no task lease, so background virtual injection
	// is refused here; the human takeover path binds an intervention lease.
	if grant.Source.Kind == protocol.MirrorSourceVirtual {
		return protocol.MirrorInputUnsupported
	}

	injector := b.d.globalInjector()
	if injector == nil || !injector.Available() {
		return protocol.MirrorInputDenied
	}

	switch msg.Kind {
	case protocol.MirrorInputPointerDown, protocol.MirrorInputPointerUp,
		protocol.MirrorInputPointerMove, protocol.MirrorInputWheel:
		if msg.Pointer == nil {
			return protocol.MirrorInputUnsupported
		}
		x, y := mapFrameToDisplay(msg.Pointer.X, msg.Pointer.Y, descriptor)
		event := globalinput.PointerEvent{
			Kind:   msg.Kind,
			Button: globalinput.Button(msg.Pointer.Button),
			X:      x, Y: y,
			DeltaX: msg.Pointer.DeltaX, DeltaY: msg.Pointer.DeltaY,
		}
		return b.dispatchPointer(injector, grant, msg.GestureID, event)
	case protocol.MirrorInputKeyDown, protocol.MirrorInputKeyUp:
		if msg.Key == nil {
			return protocol.MirrorInputUnsupported
		}
		down := msg.Kind == protocol.MirrorInputKeyDown
		return b.dispatchKey(injector, grant, msg.GestureID, globalinput.KeyEvent{Key: msg.Key.Key, Modifiers: append([]string(nil), msg.Key.Modifiers...), Down: down})
	case protocol.MirrorInputType:
		if msg.Text == nil {
			return protocol.MirrorInputUnsupported
		}
		return injectorErrorReason(injector.Text(msg.Text.Text))
	default:
		return protocol.MirrorInputUnsupported
	}
}

// mapFrameToDisplay converts observed frame pixels into the target display's
// global desktop coordinate space, applying the encoded->logical scale and the
// display's desktop origin. Native posting uses logical points.
func mapFrameToDisplay(frameX, frameY float64, s native.SourceDescriptor) (float64, float64) {
	encodedW := float64(s.Width)
	encodedH := float64(s.Height)
	if encodedW <= 0 || encodedH <= 0 {
		return frameX, frameY
	}
	logicalW := s.LogicalWidth
	logicalH := s.LogicalHeight
	if logicalW <= 0 || logicalH <= 0 {
		logicalW, logicalH = encodedW, encodedH
	}
	x := frameX/encodedW*logicalW + float64(s.X)
	y := frameY/encodedH*logicalH + float64(s.Y)
	return x, y
}

func injectorErrorReason(err error) string {
	if err == nil {
		return ""
	}
	switch err {
	case globalinput.ErrPermissionDenied:
		return protocol.MirrorInputDenied
	case globalinput.ErrUnsupported:
		return protocol.MirrorInputUnsupported
	default:
		return protocol.MirrorInputUnsupported
	}
}

var _ mirror.ControlBackend = (*mirrorControlBackend)(nil)
