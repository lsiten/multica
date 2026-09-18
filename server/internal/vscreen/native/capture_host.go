package native

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native/capture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type nativeCaptureStream interface {
	Next(context.Context) (capture.Sample, error)
	Close(context.Context) error
	ForceKeyframe(context.Context) error
	Stats() (capture.Stats, error)
	UpdateExclusions(context.Context, []uint32) error
}

type capturedStream struct {
	exclusions []uint32
	descriptor CaptureDescriptor
	options    CaptureOptions
	stream     nativeCaptureStream
	cancel     context.CancelFunc
	done       chan struct{}
	err        error
	closeErr   error
}
type captureHost struct {
	exclusions  []uint32
	enabled     bool
	token       [32]byte
	media       net.Conn
	writeMu     sync.Mutex
	mediaFailed atomic.Bool
	streams     map[string]*capturedStream
	used        map[string]bool
}

func newCaptureHost(enabled bool, token [32]byte) *captureHost {
	return &captureHost{enabled: enabled, token: token, streams: make(map[string]*capturedStream), used: make(map[string]bool)}
}

func (h *captureHost) start(request Request, sources []SourceDescriptor) (*CaptureDescriptor, error) {
	if !h.enabled || h.mediaFailed.Load() {
		return nil, errors.New("media_unavailable")
	}
	if request.Capture == nil {
		return nil, ErrProtocol
	}
	options := *request.Capture
	if raw, err := hex.DecodeString(options.StreamID); err != nil || len(raw) != 16 {
		return nil, ErrProtocol
	}
	var source SourceDescriptor
	found := false
	for _, candidate := range sources {
		if candidate.Source == options.Source {
			source = candidate
			found = true
			break
		}
	}
	if !found {
		return nil, ErrUnavailable
	}
	epoch := protocol.VscreenEpoch{NativeEpoch: source.NativeEpoch, DisplayGeneration: source.Generation, GeometryRevision: source.GeometryRevision}
	if request.Epoch != epoch {
		return nil, errors.New("stale_epoch")
	}
	if current, ok := h.streams[options.StreamID]; ok {
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
	if h.used[options.StreamID] {
		return nil, errors.New("stale_stream")
	}
	if len(h.streams) >= 16 || len(h.used) >= 4096 {
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
	requestedOptions := options
	if source.Source.Kind != protocol.MirrorSourceVirtual {
		options.ExcludedWindowIDs = append([]uint32(nil), h.exclusions...)
	} else {
		options.ExcludedWindowIDs = nil
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
		bitrate = 4000000
	}
	layout, err := capture.FitLayout(source.LogicalWidth, source.LogicalHeight, width, height)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := capture.Open(ctx, capture.Config{DisplayID: source.DisplayID, Width: width, Height: height, FPS: fps, Bitrate: bitrate, ExcludedWindowIDs: options.ExcludedWindowIDs, ShowCursor: options.ShowCursor, MaxLevelIDC: options.MaxLevelIDC})
	if err != nil {
		cancel()
		return nil, err
	}
	current := &capturedStream{descriptor: CaptureDescriptor{StreamID: options.StreamID, Source: source, Layout: layout, FPS: fps, Bitrate: bitrate, MaxLevelIDC: options.MaxLevelIDC}, options: requestedOptions, exclusions: append([]uint32(nil), options.ExcludedWindowIDs...), stream: stream, cancel: cancel, done: make(chan struct{})}
	h.streams[options.StreamID] = current
	h.used[options.StreamID] = true
	go h.deliver(ctx, current, epoch)
	return &current.descriptor, nil
}

func (h *captureHost) deliver(ctx context.Context, current *capturedStream, epoch protocol.VscreenEpoch) {
	defer close(current.done)
	defer func() {
		current.closeErr = current.stream.Close(context.WithoutCancel(ctx))
		current.err = errors.Join(current.err, current.closeErr)
		if err := h.emitTerminal(current, epoch); err != nil {
			current.err = errors.Join(current.err, err)
		}
	}()
	for {
		sample, err := current.stream.Next(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				current.err = err
			}
			return
		}
		// Metadata checks invalidate frames after display removal/geometry change; no screenshot polling.
		displays, err := listDisplays()
		if err != nil {
			current.err = err
			return
		}
		present := false
		for _, display := range displays {
			if display.ID == current.descriptor.Source.DisplayID {
				source := current.descriptor.Source
				present = "display:"+display.UUID == source.Source.SourceID && display.Width == source.Width && display.Height == source.Height && display.Scale == source.Scale && display.X == source.X && display.Y == source.Y
				break
			}
		}
		if !present {
			current.err = ErrUnavailable
			return
		}
		err = h.writeSample(MediaSample{StreamID: current.descriptor.StreamID, Epoch: epoch, DisplayID: current.descriptor.Source.DisplayID, PTSNanos: sample.PTSNanos, DurationNanos: sample.DurationNanos, KeyFrame: sample.KeyFrame, AnnexB: sample.AnnexB})
		if err != nil {
			current.err = err
			return
		}
	}
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

func (h *captureHost) emitTerminal(current *capturedStream, epoch protocol.VscreenEpoch) error {
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
	return h.writeSample(MediaSample{Kind: MediaTerminal, TerminalReason: reason, StreamID: current.descriptor.StreamID, Epoch: epoch, DisplayID: current.descriptor.Source.DisplayID})
}

func (h *captureHost) operate(request Request) (*CaptureDescriptor, error) {
	if request.Capture == nil {
		return nil, ErrProtocol
	}
	current, ok := h.streams[request.Capture.StreamID]
	if !ok {
		return nil, ErrUnavailable
	}
	source := current.descriptor.Source
	if source.Resource != request.Resource || request.Epoch != (protocol.VscreenEpoch{NativeEpoch: source.NativeEpoch, DisplayGeneration: source.Generation, GeometryRevision: source.GeometryRevision}) {
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

func (h *captureHost) stopResource(key protocol.ResourceKey) error {
	var result error
	for id, current := range h.streams {
		if current.descriptor.Source.Resource == key {
			current.cancel()
			<-current.done
			result = errors.Join(result, current.closeErr)
			if current.closeErr == nil {
				delete(h.streams, id)
			}
		}
	}
	return result
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
		if err := h.media.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			result = errors.Join(result, err)
		}
	}
	clear(h.token[:])
	return result
}

func (h *captureHost) updateExclusions(ctx context.Context, ids []uint32) error {
	if len(ids) > 32 {
		return ErrProtocol
	}
	seen := make(map[uint32]bool)
	for _, id := range ids {
		if id == 0 || seen[id] {
			return ErrProtocol
		}
		seen[id] = true
	}
	h.exclusions = append([]uint32(nil), ids...)
	for _, current := range h.streams {
		if current.descriptor.Source.Source.Kind == protocol.MirrorSourceVirtual {
			continue
		}
		select {
		case <-current.done:
			continue
		default:
		}
		if slices.Equal(current.exclusions, ids) {
			continue
		}
		if err := current.stream.UpdateExclusions(ctx, ids); err != nil {
			if errors.Is(err, capture.ErrClosed) {
				continue
			}
			return err
		}
		current.exclusions = append([]uint32(nil), ids...)
	}
	return nil
}
