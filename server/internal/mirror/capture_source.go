package mirror

import (
	"context"
	"errors"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// EncodedSource is an authorized immutable native capture binding. Epochs and
// runtime identity prevent reuse across display recreation or authorization scopes.
type EncodedSource struct {
	Binding                     protocol.MirrorSourceBinding
	GeometryRevision            uint64
	MaxLevelIDC                 uint8
	DisplayID                   uint32
	Width, Height, FPS, Bitrate int
	ShowCursor                  bool
	ExcludedWindowIDs           []uint32
}

// EncodedSample contains immutable bytes; keyframes include Annex-B SPS/PPS and IDR.
type EncodedSample struct {
	AnnexB                  []byte
	PTSNanos, DurationNanos int64
	KeyFrame                bool
}

// StreamProvider opens only the selected authorized source, without fallback.
type StreamProvider interface {
	Open(context.Context, EncodedSource) (EncodedStream, error)
}

// EncodedStream must unblock Next on context cancellation. Close terminates
// native callbacks before freeing buffers; ForceKeyframe is concurrency-safe.
type EncodedStream interface {
	Next(context.Context) (EncodedSample, error)
	ForceKeyframe() error
	Close() error
}

func (s EncodedSource) validate() error {
	if s.Binding.Resource.Validate() != nil || s.Binding.Source.Validate() != nil || s.Binding.NativeEpoch == "" || s.Binding.Generation == "" || s.GeometryRevision == 0 || s.DisplayID == 0 || s.Width < 2 || s.Width > 8192 || s.Height < 2 || s.Height > 8192 || s.FPS <= 0 || s.FPS > 120 || s.Bitrate <= 0 {
		return errors.New("mirror: invalid encoded source")
	}
	return nil
}
