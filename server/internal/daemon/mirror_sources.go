package daemon

import (
	"context"
	"errors"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/mirror/nativecapture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) handleMirrorSourcesQuery(ctx context.Context, query protocol.MirrorSourcesQuery, enqueue func([]byte) (*wsOutbound, error), generation mirrorConnectionGeneration) {
	if query.Validate() != nil || !d.mirrorEnvelopeCurrent(query.RuntimeEnvelope, generation) {
		return
	}
	result := protocol.MirrorSourcesResult{RuntimeEnvelope: query.RuntimeEnvelope}
	sources, err := d.vscreenSources(ctx, query.WorkspaceID, query.RuntimeID)
	if err != nil {
		result.Reason = protocol.MirrorSourceReason(mirrorFailureReason(err))
	} else {
		result.Sources = make([]protocol.MirrorSourceDescriptor, 0, len(sources))
		for _, source := range sources {
			result.Sources = append(result.Sources, protocol.MirrorSourceDescriptor{
				MirrorSourceBinding: source.MirrorSourceBinding,
				DisplayID:           source.DisplayID,
				Name:                source.Name,
				Width:               int(source.Width),
				Height:              int(source.Height),
				LogicalWidth:        source.LogicalWidth,
				LogicalHeight:       source.LogicalHeight,
				Scale:               source.Scale,
				X:                   source.X,
				Y:                   source.Y,
				GeometryRevision:    source.GeometryRevision,
			})
		}
	}
	if err := d.sendVscreen(enqueue, generation, protocol.EventMirrorSourcesResult, result); err != nil {
		d.logger.Debug("mirror sources result dropped", "error", err)
	}
}

func (d *Daemon) vscreenSources(ctx context.Context, workspaceID, runtimeID string) ([]nativecapture.SourceDescriptor, error) {
	if d.mirrorProcessMode() {
		var value []nativecapture.SourceDescriptor
		err := d.mirrorCall(ctx, mirrorProcessRequest{Operation: "sources", WorkspaceID: workspaceID, RuntimeID: runtimeID}, &value)
		return value, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := d.vscreenResource(workspaceID, runtimeID)
	if err != nil {
		return nil, err
	}
	runtime := d.vscreenRuntime()
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err = d.startVscreenHost(ctx, runtime); err != nil {
		return nil, err
	}
	return runtime.client.Sources(ctx, key)
}

func mirrorFailureReason(err error) string {
	if errors.Is(err, mirror.ErrCapturePermissionDenied) {
		return "permission_denied"
	}
	if errors.Is(err, mirror.ErrNoDisplay) {
		return "source_gone"
	}
	return "native_unavailable"
}
