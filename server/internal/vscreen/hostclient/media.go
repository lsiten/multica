package hostclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"math"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/capture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// EncodedSelection binds output settings to a trusted catalog entry.
type EncodedSelection struct {
	Source                                   native.SourceDescriptor
	Width, Height, FPS, Bitrate, MaxLevelIDC uint32
	ShowCursor                               bool
	ExcludedWindowIDs                        []uint32
}

func isCaptureOperation(operation string) bool {
	switch operation {
	case "start_capture", "stop_capture", "force_keyframe", "capture_status":
		return true
	default:
		return false
	}
}

func sourceEpoch(source native.SourceDescriptor) protocol.VscreenEpoch {
	return protocol.VscreenEpoch{NativeEpoch: source.NativeEpoch, DisplayGeneration: source.Generation, GeometryRevision: source.GeometryRevision}
}

func (c *Client) validSource(source native.SourceDescriptor, resource protocol.ResourceKey) bool {
	_, layoutErr := capture.FitLayout(source.LogicalWidth, source.LogicalHeight, source.Width, source.Height)
	return layoutErr == nil && source.Resource == resource && source.Source.Validate() == nil && sourceEpoch(source).Validate() == nil && source.NativeEpoch == c.epoch && source.DisplayID != 0 && source.Width > 0 && source.Height > 0 && source.LogicalWidth > 0 && source.LogicalHeight > 0 && source.Scale > 0 && !math.IsNaN(source.Scale) && !math.IsInf(source.Scale, 0)
}

func (c *Client) validateMediaResponse(request native.Request, response native.Response) error {
	if request.Operation == "sources" {
		seen := make(map[protocol.MirrorSource]bool)
		for _, source := range response.Sources {
			if !c.validSource(source, request.Resource) || seen[source.Source] {
				return native.ErrProtocol
			}
			seen[source.Source] = true
		}
		return nil
	}
	if request.Capture == nil || response.Capture == nil || response.Epoch != request.Epoch {
		return native.ErrProtocol
	}
	descriptor := response.Capture
	if descriptor.StreamID != request.Capture.StreamID || !c.validSource(descriptor.Source, request.Resource) || sourceEpoch(descriptor.Source) != request.Epoch || descriptor.Source.Source != request.Capture.Source {
		return native.ErrProtocol
	}
	c.mediaMu.Lock()
	stream := c.streams[descriptor.StreamID]
	c.mediaMu.Unlock()
	if stream == nil || descriptor.Source != stream.selection.Source {
		return native.ErrProtocol
	}
	selection := stream.selection
	layout, err := capture.FitLayout(selection.Source.LogicalWidth, selection.Source.LogicalHeight, selection.Width, selection.Height)
	if err != nil || descriptor.Layout != layout || descriptor.FPS != selection.FPS || descriptor.Bitrate != selection.Bitrate || descriptor.MaxLevelIDC != selection.MaxLevelIDC {
		return native.ErrProtocol
	}
	return nil
}

// Sources reads the runtime-scoped native source catalog without selecting a fallback.
func (c *Client) Sources(ctx context.Context, resource protocol.ResourceKey) ([]native.SourceDescriptor, error) {
	response, err := c.Call(ctx, native.Request{Operation: "sources", Resource: resource})
	return response.Sources, err
}

// OpenStream registers the identity before capture starts, so early frames cannot race registration.
func (c *Client) OpenStream(ctx context.Context, selection EncodedSelection) (*Stream, error) {
	if !c.validSource(selection.Source, selection.Source.Resource) || selection.Source.Resource.Validate() != nil || selection.Width == 0 || selection.Height == 0 || selection.Width > 8192 || selection.Height > 8192 || selection.FPS == 0 || selection.FPS > 120 || selection.Bitrate == 0 || (selection.MaxLevelIDC != 31 && selection.MaxLevelIDC != 40) {
		return nil, native.ErrProtocol
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	options := native.CaptureOptions{StreamID: hex.EncodeToString(id[:]), Source: selection.Source.Source, Width: selection.Width, Height: selection.Height, FPS: selection.FPS, Bitrate: selection.Bitrate, MaxLevelIDC: selection.MaxLevelIDC, ShowCursor: selection.ShowCursor, ExcludedWindowIDs: append([]uint32(nil), selection.ExcludedWindowIDs...)}
	stream := &Stream{startDone: make(chan struct{}), client: c, selection: selection, options: options, ready: make(chan struct{}, 1), done: make(chan struct{}), keyframes: make(chan struct{}, 1), workerDone: make(chan struct{}), waitingIDR: true}
	c.mediaMu.Lock()
	if c.media == nil || c.mediaErr != nil || c.closing || len(c.streams) >= 4096 {
		c.mediaMu.Unlock()
		return nil, ErrClosed
	}
	c.streams[options.StreamID] = stream
	c.mediaMu.Unlock()
	_, err := c.Call(ctx, stream.request("start_capture"))
	if err != nil {
		stream.finish(err)
		close(stream.workerDone)
		close(stream.startDone)
		return nil, err
	}
	stream.started = true
	close(stream.startDone)
	go stream.requestKeyframes()
	return stream, nil
}

func (c *Client) readMedia() {
	defer close(c.mediaDone)
	defer c.media.Close()
	for {
		sample, err := native.ReadMediaSample(c.media)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				err = native.ErrProtocol
			}
			c.failMedia(err)
			return
		}
		c.mediaMu.Lock()
		stream := c.streams[sample.StreamID]
		c.mediaMu.Unlock()
		if stream == nil {
			c.failMedia(native.ErrProtocol)
			c.media.Close()
			return
		}
		if sample.Epoch != sourceEpoch(stream.selection.Source) || sample.DisplayID != stream.selection.Source.DisplayID {
			stream.finish(native.ErrProtocol)
			continue
		}
		if sample.Kind == native.MediaTerminal {
			switch sample.TerminalReason {
			case native.TerminalClosed:
				stream.finish(io.EOF)
			case native.TerminalPermissionDenied:
				stream.finish(&RemoteError{Code: "screen_recording_denied"})
			case native.TerminalSourceGone:
				stream.finish(&RemoteError{Code: "source_gone"})
			case native.TerminalCaptureUnavailable:
				stream.finish(&RemoteError{Code: "capture_unavailable"})
			default:
				c.failMedia(native.ErrProtocol)
				return
			}
			continue
		}
		stream.push(sample)
	}
}

func (c *Client) failMedia(err error) {
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	if c.mediaErr != nil {
		return
	}
	c.mediaErr = err
	for _, stream := range c.streams {
		stream.finish(err)
	}
}

func (c *Client) closeStreams() {
	c.mediaMu.Lock()
	c.closing = true
	streams := make([]*Stream, 0, len(c.streams))
	for _, stream := range c.streams {
		streams = append(streams, stream)
	}
	c.mediaMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), c.shutdownTimeout)
	defer cancel()
	for _, stream := range streams {
		c.stopErr = errors.Join(c.stopErr, stream.close(ctx))
	}
}
