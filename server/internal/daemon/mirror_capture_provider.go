package daemon

import (
	"context"
	"errors"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/mirror/nativecapture"
)

// MirrorCaptureProvider adapts the read-only native capture host to CaptureHub.
type MirrorCaptureProvider struct{ Client *nativecapture.Client }

func (p MirrorCaptureProvider) Open(ctx context.Context, selected mirror.EncodedSource) (mirror.EncodedStream, error) {
	if p.Client == nil {
		return nil, nativecapture.ErrClosed
	}
	if selected.Width <= 0 || selected.Height <= 0 ||
		selected.Width > 8192 || selected.Height > 8192 ||
		selected.FPS <= 0 || selected.FPS > 120 || selected.Bitrate <= 0 ||
		uint64(selected.Bitrate) > uint64(^uint32(0)) {
		return nil, nativecapture.ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nativeCtx := context.WithoutCancel(ctx)
	sources, err := p.Client.Sources(nativeCtx, selected.Binding.Resource)
	if err != nil {
		return nil, mirrorCaptureError(err)
	}
	for _, source := range sources {
		if source.MirrorSourceBinding != selected.Binding ||
			source.DisplayID != selected.DisplayID ||
			source.GeometryRevision != selected.GeometryRevision {
			continue
		}
		stream, err := p.Client.OpenStream(nativeCtx, nativecapture.EncodedSelection{
			Source:            source,
			Width:             uint32(selected.Width),
			Height:            uint32(selected.Height),
			FPS:               uint32(selected.FPS),
			Bitrate:           uint32(selected.Bitrate),
			MaxLevelIDC:       uint32(selected.MaxLevelIDC),
			ShowCursor:        selected.ShowCursor,
			ExcludedWindowIDs: append([]uint32(nil), selected.ExcludedWindowIDs...),
		})
		if err != nil {
			return nil, mirrorCaptureError(err)
		}
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, stream.Close())
		}
		return &mirrorEncodedStream{stream}, nil
	}
	return nil, nativecapture.ErrUnavailable
}

type mirrorEncodedStream struct{ *nativecapture.Stream }

func (s *mirrorEncodedStream) Next(ctx context.Context) (mirror.EncodedSample, error) {
	sample, err := s.Stream.Next(ctx)
	if err != nil {
		return mirror.EncodedSample{}, mirrorCaptureError(err)
	}
	return mirror.EncodedSample{
		AnnexB:        sample.AnnexB,
		PTSNanos:      sample.PTSNanos,
		DurationNanos: sample.DurationNanos,
		KeyFrame:      sample.KeyFrame,
	}, nil
}

var _ mirror.StreamProvider = MirrorCaptureProvider{}

func mirrorCaptureError(err error) error {
	var remote *nativecapture.RemoteError
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
	if errors.Is(err, nativecapture.ErrUnsupported) {
		return mirror.ErrCaptureUnsupported
	}
	return err
}
