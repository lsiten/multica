package nativecapture

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"

	"github.com/multica-ai/multica/server/internal/mirror/nativecapture/capture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type EncodedSelection struct {
	Source            SourceDescriptor
	Width             uint32
	Height            uint32
	FPS               uint32
	Bitrate           uint32
	MaxLevelIDC       uint32
	ShowCursor        bool
	ExcludedWindowIDs []uint32
}

func (c *Client) validateMediaResponse(request Request, response Response) error {
	if request.Operation == "sources" {
		seen := make(map[string]bool)
		for _, source := range response.Sources {
			resource := request.Resource
			resource.DisplayID = source.DisplayID
			if !c.validSource(source, resource) || seen[source.Source.SourceID] {
				return ErrProtocol
			}
			seen[source.Source.SourceID] = true
		}
		return nil
	}
	if request.Capture == nil || response.Capture == nil || response.Epoch != request.Epoch {
		return ErrProtocol
	}
	descriptor := response.Capture
	if descriptor.StreamID != request.Capture.StreamID ||
		!c.validSource(descriptor.Source, request.Resource) ||
		sourceEpoch(descriptor.Source) != request.Epoch ||
		descriptor.Source.Source != request.Capture.Source {
		return ErrProtocol
	}
	if request.Operation == "stop_capture" {
		return nil
	}
	c.mediaMu.Lock()
	stream := c.streams[descriptor.StreamID]
	c.mediaMu.Unlock()
	if stream == nil || descriptor.Source != stream.selection.Source {
		return ErrProtocol
	}
	layout, err := capture.FitLayout(stream.selection.Source.LogicalWidth, stream.selection.Source.LogicalHeight, stream.selection.Width, stream.selection.Height)
	if err != nil || descriptor.Layout != layout || descriptor.FPS != stream.selection.FPS ||
		descriptor.Bitrate != stream.selection.Bitrate || descriptor.MaxLevelIDC != stream.selection.MaxLevelIDC {
		return ErrProtocol
	}
	return nil
}

func (c *Client) Sources(ctx context.Context, resource protocol.ResourceKey) ([]SourceDescriptor, error) {
	response, err := c.Call(ctx, Request{Operation: "sources", Resource: resource})
	return response.Sources, err
}

func (c *Client) OpenStream(ctx context.Context, selection EncodedSelection) (*Stream, error) {
	if !c.validSource(selection.Source, selection.Source.Resource) || selection.Width == 0 ||
		selection.Height == 0 || selection.Width > 8192 || selection.Height > 8192 ||
		selection.FPS == 0 || selection.FPS > 120 || selection.Bitrate == 0 ||
		(selection.MaxLevelIDC != 31 && selection.MaxLevelIDC != 40) {
		return nil, ErrProtocol
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	options := CaptureOptions{
		StreamID: hex.EncodeToString(id[:]), Source: selection.Source.Source,
		Width: selection.Width, Height: selection.Height, FPS: selection.FPS,
		Bitrate: selection.Bitrate, MaxLevelIDC: selection.MaxLevelIDC,
		ShowCursor:        selection.ShowCursor,
		ExcludedWindowIDs: append([]uint32(nil), selection.ExcludedWindowIDs...),
	}
	stream := &Stream{
		startDone: make(chan struct{}), client: c, selection: selection, options: options,
		ready: make(chan struct{}, 1), done: make(chan struct{}),
		keyframes: make(chan struct{}, 1), workerDone: make(chan struct{}), waitingIDR: true,
	}
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
		sample, err := ReadMediaSample(c.media)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				err = ErrProtocol
			}
			c.failMedia(err)
			return
		}
		c.mediaMu.Lock()
		stream := c.streams[sample.StreamID]
		c.mediaMu.Unlock()
		if stream == nil {
			c.failMedia(ErrProtocol)
			c.media.Close()
			return
		}
		if sample.Epoch != sourceEpoch(stream.selection.Source) ||
			sample.DisplayID != stream.selection.Source.DisplayID {
			stream.finish(ErrProtocol)
			continue
		}
		if sample.Kind == MediaTerminal {
			switch sample.TerminalReason {
			case TerminalClosed:
				stream.finish(io.EOF)
			case TerminalPermissionDenied:
				stream.finish(&RemoteError{Code: "screen_recording_denied"})
			case TerminalSourceGone:
				stream.finish(&RemoteError{Code: "source_gone"})
			case TerminalCaptureUnavailable:
				stream.finish(&RemoteError{Code: "capture_unavailable"})
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
