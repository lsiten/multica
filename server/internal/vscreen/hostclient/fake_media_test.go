//go:build darwin || linux

package hostclient

import (
	"bytes"
	"io"
	"net"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/capture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func fakeSources(key protocol.ResourceKey) []native.SourceDescriptor {
	sources := make([]native.SourceDescriptor, 2)
	for i := range sources {
		sources[i] = native.SourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: key, Source: protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: []string{"display:one", "display:two"}[i]}, NativeEpoch: strings.Repeat("a", 64), Generation: strings.Repeat("b", 64)}, DisplayID: uint32(i + 1), Width: 1600, Height: 900, LogicalWidth: 1600, LogicalHeight: 900, Scale: 1, GeometryRevision: 1}
	}
	return sources
}

func fakeMediaHost(control net.Conn, response native.Response, token []byte, mode string) int {
	file := os.NewFile(5, "media")
	media, err := net.FileConn(file)
	file.Close()
	if err != nil {
		return 30
	}
	defer media.Close()
	capability := make([]byte, 32)
	if _, err := io.ReadFull(media, capability); err != nil || !bytes.Equal(capability, token) {
		return 31
	}
	streams := make(map[string]native.CaptureDescriptor)
	var order []string
	write := func(id string, pts int64, key bool) error {
		descriptor := streams[id]
		sample := native.MediaSample{StreamID: id, Epoch: sourceEpoch(descriptor.Source), DisplayID: descriptor.Source.DisplayID, PTSNanos: pts, DurationNanos: 33333333, KeyFrame: key, AnnexB: []byte{0, 0, 0, 1, 0x65}}
		if mode == "media-display" && pts == 99 {
			sample.DisplayID++
		}
		if mode == "media-generation" && pts == 99 {
			sample.Epoch.DisplayGeneration = strings.Repeat("c", 64)
		}
		if mode == "media-epoch" && pts == 99 {
			sample.Epoch.NativeEpoch = strings.Repeat("c", 64)
		}
		if mode == "media-stale" && pts == 99 {
			sample.Epoch.GeometryRevision++
		}
		if mode == "media-unknown" && pts == 99 {
			sample.StreamID = strings.Repeat("f", 32)
		}
		return native.WriteMediaSample(media, sample)
	}
	for {
		var request native.Request
		if err := native.ReadMessage(control, &request); err != nil {
			return 0
		}
		response.ID = request.ID
		response.Capture = nil
		response.Sources = nil
		response.Error = ""
		response.Epoch = protocol.VscreenEpoch{NativeEpoch: strings.Repeat("a", 64)}
		var after func() error
		switch request.Operation {
		case "sources":
			response.Sources = fakeSources(request.Resource)
		case "start_capture":
			if mode == "media-denied" {
				response.Error = "screen recording permission denied"
				break
			}
			o := request.Capture
			if mode == "media-adapter" && (!o.ShowCursor || len(o.ExcludedWindowIDs) != 1 || o.ExcludedWindowIDs[0] != 123 || o.Width != 1280 || o.Height != 720 || o.FPS != 30 || o.Bitrate != 4000000 || o.MaxLevelIDC != 31) {
				response.Error = "geometry_conflict"
				break
			}
			sources := fakeSources(request.Resource)
			var source native.SourceDescriptor
			for _, s := range sources {
				if s.Source == o.Source {
					source = s
				}
			}
			layout, _ := capture.FitLayout(source.LogicalWidth, source.LogicalHeight, o.Width, o.Height)
			d := native.CaptureDescriptor{StreamID: o.StreamID, Source: source, Layout: layout, FPS: o.FPS, Bitrate: o.Bitrate, MaxLevelIDC: o.MaxLevelIDC}
			streams[o.StreamID] = d
			order = append(order, o.StreamID)
			response.Capture = &d
			if mode == "media-response" {
				response.Capture.FPS++
			}
			response.Epoch = request.Epoch
			after = func() error { return write(o.StreamID, 1, true) }
		case "stop_capture", "force_keyframe", "capture_status":
			d := streams[request.Capture.StreamID]
			response.Capture = &d
			response.Epoch = request.Epoch
			if request.Operation == "force_keyframe" {
				after = func() error { return write(d.StreamID, 100, true) }
			}
		case "list":
			switch mode {
			case "media-terminal", "media-terminal-closed", "media-terminal-denied", "media-terminal-unavailable":
				d := streams[order[0]]
				reason := native.TerminalSourceGone
				switch mode {
				case "media-terminal-closed":
					reason = native.TerminalClosed
				case "media-terminal-denied":
					reason = native.TerminalPermissionDenied
				case "media-terminal-unavailable":
					reason = native.TerminalCaptureUnavailable
				}
				if err := native.WriteMediaSample(media, native.MediaSample{Kind: native.MediaTerminal, TerminalReason: reason, StreamID: d.StreamID, Epoch: sourceEpoch(d.Source), DisplayID: d.Source.DisplayID}); err != nil {
					return 37
				}
			case "media-eof":
				media.Close()
			case "media-malformed":
				media.Write(make([]byte, native.MediaHeaderBytes))
			case "media-stale", "media-unknown", "media-display", "media-generation", "media-epoch":
				if err := write(order[0], 99, true); err != nil {
					return 32
				}
			case "media-overflow":
				for i := int64(2); i < 20; i++ {
					if err := write(order[0], i, false); err != nil {
						return 33
					}
				}
				if len(order) > 1 {
					if err := write(order[1], 99, true); err != nil {
						return 34
					}
				}
			}
		}
		if err := native.WriteMessage(control, response); err != nil {
			return 35
		}
		if after != nil {
			if err := after(); err != nil {
				return 36
			}
		}
	}
}
