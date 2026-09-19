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
}

type cachedCatalog struct {
	sources []native.SourceDescriptor
	at      time.Time
}

func newMirrorControlBackend(d *Daemon) *mirrorControlBackend {
	return &mirrorControlBackend{d: d, catalog: make(map[string]cachedCatalog)}
}

// SetHumanInteractionEnabled toggles the host master switch for remote human
// control. It never affects agent actions.
func (d *Daemon) SetHumanInteractionEnabled(enabled bool) {
	d.humanInteractionEnabled.Store(enabled)
}

func (d *Daemon) HumanInteractionEnabled() bool {
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

// describeSource resolves one granted source against a current native catalog,
// reusing the cache and falling back to one bounded native-host query.
func (b *mirrorControlBackend) describeSource(workspaceID, runtimeID string, source protocol.MirrorSource, nativeEpoch, generation string) (native.SourceDescriptor, protocol.ResourceKey, bool) {
	var sources []native.SourceDescriptor
	if cached, ok := b.catalogFor(runtimeID); ok {
		sources = cached
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		fresh, err := b.d.vscreenSources(ctx, workspaceID, runtimeID)
		if err != nil {
			return native.SourceDescriptor{}, protocol.ResourceKey{}, false
		}
		b.remember(runtimeID, fresh)
		sources = fresh
	}
	for _, s := range sources {
		if s.Source == source && s.NativeEpoch == nativeEpoch && s.Generation == generation {
			return s, s.Resource, true
		}
	}
	return native.SourceDescriptor{}, protocol.ResourceKey{}, false
}

func (b *mirrorControlBackend) ResourceForGrant(grant protocol.MirrorControlGrant) (protocol.ResourceKey, bool) {
	return b.resourceForSourceScoped(grant.WorkspaceID, grant.RuntimeID, grant.Source, grant.NativeEpoch, grant.SourceGeneration)
}

func (b *mirrorControlBackend) resourceForSourceScoped(workspaceID, runtimeID string, source protocol.MirrorSource, nativeEpoch, generation string) (protocol.ResourceKey, bool) {
	if workspaceID == "" || runtimeID == "" {
		return protocol.ResourceKey{}, false
	}
	_, resource, ok := b.describeSource(workspaceID, runtimeID, source, nativeEpoch, generation)
	return resource, ok
}

// DispatchInput applies one accepted gesture. It returns "" on success or a
// protocol.MirrorInput* nack reason.
func (b *mirrorControlBackend) DispatchInput(_ context.Context, resource protocol.ResourceKey, grant protocol.MirrorControlGrant, msg protocol.MirrorInputMessage) string {
	descriptor, _, ok := b.describeSource(grant.WorkspaceID, grant.RuntimeID, grant.Source, grant.NativeEpoch, grant.SourceGeneration)
	if !ok {
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
		err := injector.Pointer(globalinput.PointerEvent{
			Kind:   msg.Kind,
			Button: globalinput.Button(msg.Pointer.Button),
			X:      x, Y: y,
			DeltaX: msg.Pointer.DeltaX, DeltaY: msg.Pointer.DeltaY,
		})
		return injectorErrorReason(err)
	case protocol.MirrorInputKeyDown, protocol.MirrorInputKeyUp:
		if msg.Key == nil {
			return protocol.MirrorInputUnsupported
		}
		down := msg.Kind == protocol.MirrorInputKeyDown
		err := injector.Key(globalinput.KeyEvent{Key: msg.Key.Key, Modifiers: append([]string(nil), msg.Key.Modifiers...), Down: down})
		return injectorErrorReason(err)
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
