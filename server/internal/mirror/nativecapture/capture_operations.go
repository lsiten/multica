package nativecapture

import (
	"context"
	"errors"

	"github.com/multica-ai/multica/server/internal/mirror/nativecapture/capture"
)

func (h *captureHost) operate(request Request) (*CaptureDescriptor, error) {
	if request.Capture == nil {
		return nil, ErrProtocol
	}
	current := h.streams[request.Capture.StreamID]
	if current == nil {
		return nil, ErrUnavailable
	}
	source := current.descriptor.Source
	if source.Resource != request.Resource ||
		request.Epoch != (Epoch{NativeEpoch: source.NativeEpoch, DisplayGeneration: source.Generation, GeometryRevision: source.GeometryRevision}) {
		return nil, errors.New("stale_epoch")
	}
	switch request.Operation {
	case "stop_capture":
		current.cancel()
		<-current.done
		if current.closeErr != nil {
			return nil, current.closeErr
		}
		delete(h.streams, request.Capture.StreamID)
		return &current.descriptor, nil
	case "force_keyframe":
		if err := current.stream.ForceKeyframe(context.Background()); err != nil {
			return nil, err
		}
	case "capture_status":
	default:
		return nil, ErrProtocol
	}
	select {
	case <-current.done:
		if current.err != nil {
			return nil, current.err
		}
		return nil, capture.ErrClosed
	default:
		descriptor := current.descriptor
		stats, err := current.stream.Stats()
		if err != nil {
			return nil, err
		}
		descriptor.Encoder = &stats
		return &descriptor, nil
	}
}

func (h *captureHost) close() error {
	var result error
	for _, current := range h.streams {
		current.cancel()
	}
	for _, current := range h.streams {
		<-current.done
		result = errors.Join(result, current.closeErr)
	}
	if h.media != nil {
		result = errors.Join(result, h.media.Close())
	}
	clear(h.token[:])
	return result
}
