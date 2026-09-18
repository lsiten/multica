package daemon

import (
	"context"
	"errors"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
)

// VscreenCaptureProvider adapts the supervised native host to the shared CaptureHub.
// The catalog remains authoritative even when callers hold an older selection.
type VscreenCaptureProvider struct{ Client *hostclient.Client }

// Open revalidates the exact source binding before creating a native stream.
func (p VscreenCaptureProvider) Open(ctx context.Context, selected mirror.EncodedSource) (mirror.EncodedStream, error) {
	if p.Client == nil {
		return nil, hostclient.ErrClosed
	}
	if selected.Width <= 0 || selected.Height <= 0 || selected.Width > 8192 || selected.Height > 8192 || selected.FPS <= 0 || selected.FPS > 120 || selected.Bitrate <= 0 || uint64(selected.Bitrate) > uint64(^uint32(0)) {
		return nil, native.ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Viewer cancellation must not abort the shared helper control stream. Its
	// own call timeout remains the native failure boundary.
	nativeCtx := context.WithoutCancel(ctx)
	sources, err := p.Client.Sources(nativeCtx, selected.Binding.Resource)
	if err != nil {
		return nil, vscreenCaptureError(err)
	}
	for _, source := range sources {
		if source.MirrorSourceBinding != selected.Binding || source.DisplayID != selected.DisplayID || source.GeometryRevision != selected.GeometryRevision {
			continue
		}
		stream, err := p.Client.OpenStream(nativeCtx, hostclient.EncodedSelection{Source: source, Width: uint32(selected.Width), Height: uint32(selected.Height), FPS: uint32(selected.FPS), Bitrate: uint32(selected.Bitrate), MaxLevelIDC: uint32(selected.MaxLevelIDC), ShowCursor: selected.ShowCursor, ExcludedWindowIDs: selected.ExcludedWindowIDs})
		if err != nil {
			return nil, vscreenCaptureError(err)
		}
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, stream.Close())
		}
		return &vscreenEncodedStream{stream}, nil
	}
	return nil, native.ErrUnavailable
}

type vscreenEncodedStream struct{ *hostclient.Stream }

func (s *vscreenEncodedStream) Next(ctx context.Context) (mirror.EncodedSample, error) {
	sample, err := s.Stream.Next(ctx)
	if err != nil {
		return mirror.EncodedSample{}, vscreenCaptureError(err)
	}
	return mirror.EncodedSample{AnnexB: sample.AnnexB, PTSNanos: sample.PTSNanos, DurationNanos: sample.DurationNanos, KeyFrame: sample.KeyFrame}, nil
}

var _ mirror.StreamProvider = VscreenCaptureProvider{}

func vscreenCaptureError(err error) error {
	var remote *hostclient.RemoteError
	if errors.As(err, &remote) {
		switch remote.Code {
		case "screen_recording_denied":
			return mirror.ErrCapturePermissionDenied
		case "source_gone", "display_unavailable":
			return mirror.ErrNoDisplay
		default:
			return mirror.ErrCaptureUnavailable
		}
	}
	if errors.Is(err, native.ErrUnsupported) {
		return mirror.ErrCaptureUnsupported
	}
	return err
}
