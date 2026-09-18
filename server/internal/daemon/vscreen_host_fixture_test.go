//go:build darwin || linux

package daemon

import (
	"bytes"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/capture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "internal-vscreen-host" {
		os.Exit(vscreenTestHost())
	}
	os.Exit(m.Run())
}

// vscreenTestHost is a test-owned inherited-socket process, never a user CLI.
// It models display readback and denies recording by default without any TCC call.
func vscreenTestHost() int {
	bootstrap := os.NewFile(4, "bootstrap")
	token := make([]byte, 32)
	if _, err := io.ReadFull(bootstrap, token); err != nil {
		return 2
	}
	bootstrap.Close()
	f := os.NewFile(3, "control")
	control, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return 3
	}
	defer control.Close()
	f = os.NewFile(5, "media")
	media, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return 4
	}
	defer media.Close()
	var hello native.Request
	if native.ReadMessage(control, &hello) != nil || hello.Operation != "hello" || !bytes.Equal(hello.Token, token) || hello.Build != "fixture/commit" {
		return 5
	}
	epoch := protocol.VscreenEpoch{NativeEpoch: strings.Repeat("a", 64), DisplayGeneration: strings.Repeat("b", 64), GeometryRevision: 1}
	response := native.Response{Version: 1, Build: hello.Build, ID: hello.ID, Epoch: epoch}
	if native.WriteMessage(control, response) != nil {
		return 6
	}
	got := make([]byte, 32)
	if _, err = io.ReadFull(media, got); err != nil || !bytes.Equal(got, token) {
		return 7
	}
	var mediaMu sync.Mutex
	if hello.AppControl {
		stop, err := startVscreenTestAppHost(token, media, &mediaMu)
		if err != nil {
			return 8
		}
		defer stop()
	}
	enabled := map[protocol.ResourceKey]bool{}
	geometry := map[protocol.ResourceKey]uint64{}
	streams := map[string]native.CaptureDescriptor{}
	for {
		var request native.Request
		if native.ReadMessage(control, &request) != nil {
			return 0
		}
		epoch.GeometryRevision = 1
		if revision := geometry[request.Resource]; revision != 0 {
			epoch.GeometryRevision = revision
		}
		response = native.Response{Version: 1, Build: hello.Build, ID: request.ID, Epoch: epoch}
		physical := native.SourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: request.Resource, Source: protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "display:physical"}, NativeEpoch: epoch.NativeEpoch, Generation: epoch.DisplayGeneration, Primary: true}, DisplayID: 1, Name: "Fixture physical", Width: 1600, Height: 900, LogicalWidth: 1600, LogicalHeight: 900, Scale: 1, GeometryRevision: 1}
		virtual := physical
		virtual.Source = protocol.MirrorSource{Kind: protocol.MirrorSourceVirtual, SourceID: "display:" + request.Resource.RuntimeID}
		virtual.Primary = false
		virtual.DisplayID = 2
		display := native.Display{ID: 2, UUID: virtual.Source.SourceID, Width: 1600, Height: 900, LogicalWidth: 1600, LogicalHeight: 900, Scale: 1, ScreenRecording: false}
		var sample *native.MediaSample
		switch request.Operation {
		case "update_exclusions":
			if len(request.ExcludedWindowIDs) > 32 {
				response.Error = "capture_update_failed"
			}
		case "ensure":
			enabled[request.Resource] = true
			response.Display = &display
		case "describe", "quiesce":
			if os.Getenv("VSCREEN_FIXTURE_GEOMETRY_AT") != "" {
				if request.Epoch != epoch {
					response.Error = "stale_epoch"
					break
				}
				if request.Operation == os.Getenv("VSCREEN_FIXTURE_GEOMETRY_AT") && request.Resource.RuntimeID == "rt" {
					geometry[request.Resource] = 2
					response.Epoch.GeometryRevision = 2
				}
			}
			if !enabled[request.Resource] {
				response.Error = "display_unavailable"
			} else {
				response.Display = &display
				response.Quiescent = request.Operation == "quiesce"
			}
		case "dispose":
			if os.Getenv("VSCREEN_FIXTURE_GEOMETRY_AT") != "" && request.Epoch != epoch {
				response.Error = "stale_epoch"
				break
			}
			delete(enabled, request.Resource)
			response.Quiescent = true
		case "sources":
			if barrier := os.Getenv("VSCREEN_FIXTURE_SOURCES_BARRIER"); barrier != "" {
				if err := os.WriteFile(barrier, []byte("entered"), 0600); err != nil {
					return 11
				}
				ticker := time.NewTicker(time.Millisecond)
				for {
					if _, err := os.Stat(barrier + ".release"); err == nil {
						break
					}
					<-ticker.C
				}
				ticker.Stop()
			}
			response.Sources = []native.SourceDescriptor{physical}
			if enabled[request.Resource] {
				response.Sources = append(response.Sources, virtual)
			}
		case "start_capture":
			options := request.Capture
			selected := physical
			if options.Source == virtual.Source {
				selected = virtual
			}
			layout, err := capture.FitLayout(selected.LogicalWidth, selected.LogicalHeight, options.Width, options.Height)
			if err != nil {
				return 8
			}
			descriptor := native.CaptureDescriptor{StreamID: options.StreamID, Source: selected, Layout: layout, FPS: options.FPS, Bitrate: options.Bitrate, MaxLevelIDC: options.MaxLevelIDC}
			streams[options.StreamID] = descriptor
			response.Capture = &descriptor
			sample = &native.MediaSample{StreamID: options.StreamID, Epoch: epoch, DisplayID: selected.DisplayID, PTSNanos: 1, DurationNanos: 33333333, KeyFrame: true, AnnexB: []byte{0, 0, 0, 1, 0x65, 1}}
		case "stop_capture", "force_keyframe", "capture_status":
			descriptor := streams[request.Capture.StreamID]
			response.Capture = &descriptor
			if request.Operation == "force_keyframe" {
				sample = &native.MediaSample{StreamID: descriptor.StreamID, Epoch: epoch, DisplayID: descriptor.Source.DisplayID, PTSNanos: 100, DurationNanos: 33333333, KeyFrame: true, AnnexB: []byte{0, 0, 0, 1, 0x65, 1}}
			}
		default:
			response.Error = "operation_unsupported"
		}
		if native.WriteMessage(control, response) != nil {
			return 9
		}
		if sample != nil {
			mediaMu.Lock()
			writeErr := native.WriteMediaSample(media, *sample)
			mediaMu.Unlock()
			if writeErr != nil {
				return 10
			}
		}
	}
}
