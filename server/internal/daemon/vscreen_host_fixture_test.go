//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/applicationhost"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/capture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == applicationhost.Entrypoint && os.Getenv("APPLICATION_DAEMON_FIXTURE") == "1" {
		if path := os.Getenv("APPLICATION_HOST_PID_FILE"); path != "" {
			if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
				os.Exit(96)
			}
		}
		if err := applicationhost.Run(context.Background(), os.Args[2]); err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(95)
		}
		os.Exit(0)
	}

	if len(os.Args) == 2 && os.Args[1] == runtimeproc.Entrypoint {
		globalInjectorOnce.Do(func() {
			globalInjectorInst = &physicalInjectorFixture{available: os.Getenv("MIRROR_FIXTURE_INPUT") == "enabled"}
		})
		bootstrap, err := runtimeproc.ReadBootstrap(os.Stdin, "fixture/commit")
		if err != nil {
			os.Exit(92)
		}
		if os.Getenv("MULTICA_TOKEN") != "" {
			os.Exit(93)
		}
		if bootstrap.Identity.Scope.Service == "environment" {
			err = RunEnvironmentService(context.Background(), bootstrap)
		} else if bootstrap.Identity.Scope.Service == "application" {
			err = RunApplicationService(context.Background(), bootstrap)
		} else if bootstrap.Identity.Scope.Service == "gateway" {
			err = RunGatewayService(context.Background(), bootstrap)
		} else if bootstrap.Identity.Scope.Service == "worker" {
			err = RunWorkerService(context.Background(), bootstrap)
		} else if bootstrap.Identity.Scope.Service == "control" {
			err = RunControlService(context.Background(), bootstrap)
		} else if os.Getenv("MIRROR_CANCEL_ADAPTER_FIXTURE") == "1" {
			err = runMirrorCancelAdapterFixture(context.Background(), bootstrap)
		} else {
			err = RunMirrorService(context.Background(), bootstrap)
		}
		if err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(94)
		}
		os.Exit(0)
	}
	if len(os.Args) == 5 && os.Args[1] == "readopt-parent" {
		// A plain parent (not a runtimeproc owner) that starts one real child via
		// runtimeproc.Start(context.Background()), records the child PID and its
		// bootstrap, and then exits WITHOUT closing the child. The child is
		// orphaned and reparented to init; the test verifies it survives and is
		// re-adopted via runtimeproc.Open. See control_readopt_runtime_test.go.
		if err := runReAdoptParent(os.Args[2], os.Args[3], os.Args[4]); err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(98)
		}
		os.Exit(0)
	}
	if len(os.Args) == 4 && os.Args[1] == "survival-parent" {
		// A plain parent (not a runtimeproc owner) that starts one real child via
		// runtimeproc.Start(context.Background()), records the child's PID, and then
		// exits WITHOUT closing the child. The child is orphaned and reparented to
		// init; the test verifies it survives. See control_survival_runtime_test.go.
		if err := runSurvivalParent(os.Args[2], os.Args[3]); err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(97)
		}
		os.Exit(0)
	}
	if len(os.Args) >= 3 && os.Args[1] == VscreenSmokeProviderCommand {
		if err := RunVscreenSmokeProvider(context.Background(), os.Args[2], os.Args[3:], os.Stdin, os.Stdout); err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "vscreen-provider-fixture" {
		os.Exit(runVscreenProviderFixture())
	}
	if len(os.Args) == 2 && os.Args[1] == "internal-vscreen-host" {
		os.Exit(vscreenTestHost())
	}
	os.Exit(m.Run())
}

// vscreenTestHost is a test-owned inherited-socket process, never a user CLI.
// It models display readback and denies recording by default without any TCC call.
func vscreenTestHost() int {
	if name := os.Getenv("MIRROR_FIXTURE_NATIVE_PID"); name != "" {
		if err := os.WriteFile(name, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			return 12
		}
	}
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
		physical.Resource.DisplayID = physical.DisplayID
		virtual.Resource.DisplayID = 0
		virtual.Source = protocol.MirrorSource{Kind: protocol.MirrorSourceVirtual, SourceID: "display:" + request.Resource.RuntimeID}
		virtual.Primary = false
		virtual.DisplayID = 2
		display := native.Display{ID: 2, Managed: true, UUID: virtual.Source.SourceID, Width: 1600, Height: 900, LogicalWidth: 1600, LogicalHeight: 900, Scale: 1, ScreenRecording: false}
		var sample *native.MediaSample
		switch request.Operation {
		case "update_exclusions":
			if len(request.ExcludedWindowIDs) > 32 {
				response.Error = "capture_update_failed"
			}
		case "list":
			response.Displays = []native.Display{}
			for _, active := range enabled {
				if active {
					response.Displays = append(response.Displays, display)
					break
				}
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
			if barrier := os.Getenv("VSCREEN_FIXTURE_SOURCES_BARRIER"); barrier != "" && mirrorFixtureBarrierArmed() {
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
			if os.Getenv("MIRROR_FIXTURE_CAPTURE_DENIED") == "1" {
				response.Error = "screen_recording_denied"
				break
			}
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

func mirrorFixtureBarrierArmed() bool {
	path := os.Getenv("MIRROR_FIXTURE_ARM_BARRIER")
	if path == "" {
		return true
	}
	_, err := os.Stat(path)
	return err == nil
}
