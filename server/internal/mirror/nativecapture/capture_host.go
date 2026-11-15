package nativecapture

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror/nativecapture/capture"
)

type nativeCaptureStream interface {
	Next(context.Context) (capture.Sample, error)
	Close(context.Context) error
	ForceKeyframe(context.Context) error
	Stats() (capture.Stats, error)
	UpdateExclusions(context.Context, []uint32) error
}

type capturedStream struct {
	descriptor CaptureDescriptor
	options    CaptureOptions
	stream     nativeCaptureStream
	cancel     context.CancelFunc
	done       chan struct{}
	err        error
	closeErr   error
}

type captureHost struct {
	token       [32]byte
	media       net.Conn
	writeMu     sync.Mutex
	mediaFailed atomic.Bool
	streams     map[string]*capturedStream
	used        map[string]bool
}

func newCaptureHost(token [32]byte) *captureHost {
	return &captureHost{token: token, streams: make(map[string]*capturedStream), used: make(map[string]bool)}
}

func (h *captureHost) start(request Request, sources []SourceDescriptor) (*CaptureDescriptor, error) {
	if h.mediaFailed.Load() || request.Capture == nil {
		return nil, ErrUnavailable
	}
	options := *request.Capture
	if raw, err := hex.DecodeString(options.StreamID); err != nil || len(raw) != 16 {
		return nil, ErrProtocol
	}
	var source SourceDescriptor
	found := false
	for _, candidate := range sources {
		if candidate.Source == options.Source {
			source, found = candidate, true
			break
		}
	}
	if !found {
		return nil, ErrUnavailable
	}
	if request.Epoch != (Epoch{NativeEpoch: source.NativeEpoch, DisplayGeneration: source.Generation, GeometryRevision: source.GeometryRevision}) {
		return nil, errors.New("stale_epoch")
	}
	if current := h.streams[options.StreamID]; current != nil {
		if current.descriptor.Source != source || !reflect.DeepEqual(current.options, options) {
			return nil, errors.New("capture_conflict")
		}
		select {
		case <-current.done:
			if current.err != nil {
				return nil, current.err
			}
			return nil, capture.ErrClosed
		default:
			return &current.descriptor, nil
		}
	}
	if h.used[options.StreamID] || len(h.streams) >= 16 || len(h.used) >= 4096 {
		return nil, errors.New("capture_limit")
	}
	if h.media == nil {
		connection, err := openMediaParent(h.token)
		if err != nil {
			return nil, errors.New("media_unavailable")
		}
		h.media = connection
		clear(h.token[:])
	}
	width, height, fps, bitrate := options.Width, options.Height, options.FPS, options.Bitrate
	if width == 0 {
		width = 1600
	}
	if height == 0 {
		height = 900
	}
	if fps == 0 {
		fps = 30
	}
	if bitrate == 0 {
		bitrate = 4_000_000
	}
	layout, err := capture.FitLayout(source.LogicalWidth, source.LogicalHeight, width, height)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := capture.Open(ctx, capture.Config{
		DisplayID:         source.DisplayID,
		Width:             width,
		Height:            height,
		FPS:               fps,
		Bitrate:           bitrate,
		ShowCursor:        options.ShowCursor,
		MaxLevelIDC:       options.MaxLevelIDC,
		ExcludedWindowIDs: append([]uint32(nil), options.ExcludedWindowIDs...),
	})
	if err != nil {
		cancel()
		return nil, err
	}
	descriptor := CaptureDescriptor{StreamID: options.StreamID, Source: source, Layout: layout, FPS: fps, Bitrate: bitrate, MaxLevelIDC: options.MaxLevelIDC}
	current := &capturedStream{descriptor: descriptor, options: options, stream: stream, cancel: cancel, done: make(chan struct{})}
	h.streams[options.StreamID] = current
	h.used[options.StreamID] = true
	go h.deliver(ctx, current)
	return &descriptor, nil
}

func (h *captureHost) deliver(ctx context.Context, current *capturedStream) {
	defer close(current.done)
	defer func() {
		current.closeErr = current.stream.Close(context.WithoutCancel(ctx))
		current.err = errors.Join(current.err, current.closeErr)
		_ = h.emitTerminal(current)
	}()
	for {
		sample, err := current.stream.Next(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				current.err = err
			}
			return
		}
		displays, err := listDisplays()
		if err != nil || !sourcePresent(displays, current.descriptor.Source) {
			current.err = ErrUnavailable
			return
		}
		err = h.writeSample(MediaSample{
			StreamID:      current.descriptor.StreamID,
			Kind:          MediaVideo,
			Epoch:         Epoch{NativeEpoch: current.descriptor.Source.NativeEpoch, DisplayGeneration: current.descriptor.Source.Generation, GeometryRevision: current.descriptor.Source.GeometryRevision},
			DisplayID:     current.descriptor.Source.DisplayID,
			PTSNanos:      sample.PTSNanos,
			DurationNanos: sample.DurationNanos,
			KeyFrame:      sample.KeyFrame,
			AnnexB:        sample.AnnexB,
		})
		if err != nil {
			current.err = err
			return
		}
	}
}

func sourcePresent(displays []Display, source SourceDescriptor) bool {
	for _, display := range displays {
		if display.ID == source.DisplayID && "display:"+display.UUID == source.Source.SourceID &&
			display.Width == source.Width && display.Height == source.Height &&
			display.Scale == source.Scale && display.X == source.X && display.Y == source.Y {
			return true
		}
	}
	return false
}

func (h *captureHost) writeSample(sample MediaSample) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if h.mediaFailed.Load() {
		return net.ErrClosed
	}
	err := h.media.SetWriteDeadline(time.Now().Add(time.Second))
	if err == nil {
		err = WriteMediaSample(h.media, sample)
	}
	if err != nil {
		h.mediaFailed.Store(true)
		h.media.Close()
	}
	return err
}

func (h *captureHost) emitTerminal(current *capturedStream) error {
	if h.mediaFailed.Load() {
		return nil
	}
	reason := TerminalClosed
	switch {
	case current.closeErr != nil:
		reason = TerminalCaptureUnavailable
	case errors.Is(current.err, capture.ErrPermission):
		reason = TerminalPermissionDenied
	case errors.Is(current.err, ErrUnavailable), errors.Is(current.err, capture.ErrDisplay):
		reason = TerminalSourceGone
	case current.err != nil:
		reason = TerminalCaptureUnavailable
	}
	return h.writeSample(MediaSample{
		StreamID:       current.descriptor.StreamID,
		Kind:           MediaTerminal,
		TerminalReason: reason,
		Epoch:          Epoch{NativeEpoch: current.descriptor.Source.NativeEpoch, DisplayGeneration: current.descriptor.Source.Generation, GeometryRevision: current.descriptor.Source.GeometryRevision},
		DisplayID:      current.descriptor.Source.DisplayID,
	})
}
