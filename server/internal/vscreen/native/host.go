package native

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/appclaim"
	"github.com/multica-ai/multica/server/internal/vscreen/native/capture"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type resourceDisplay struct {
	display   Display
	epoch     protocol.VscreenEpoch
	quiescent bool
	claim     *appclaim.Lock
}

// RunHost serves only an inherited private socket. Call on the initial locked OS thread.
// EOF on that socket disposes owned displays before the AppKit runloop exits.
func RunHost(build string) error { return runHost(build, false) }

func runHost(build string, qualification bool) error {
	if !supported() {
		return ErrUnsupported
	}
	if err := prepareDescriptors(); err != nil {
		return err
	}
	socket := os.NewFile(3, "vscreen-parent")
	if socket == nil {
		return ErrProtocol
	}
	defer socket.Close()
	bootstrap := os.NewFile(4, "vscreen-bootstrap")
	if bootstrap == nil {
		return ErrProtocol
	}
	defer bootstrap.Close()
	if err := validateParent(socket, bootstrap); err != nil {
		return err
	}
	if err := bootstrap.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	var scope *inputQualification
	if qualification {
		var err error
		scope, err = readInputQualification()
		if err != nil {
			return err
		}
	}
	var token [32]byte
	if _, err := io.ReadFull(bootstrap, token[:]); err != nil {
		return fmt.Errorf("read bootstrap: %w", err)
	}
	connection, err := net.FileConn(socket)
	if err != nil {
		return err
	}
	defer connection.Close()
	done := make(chan error, 1)
	go func() { done <- serveScoped(connection, token, build, scope); stopLoop() }()
	runLoop()
	return <-done
}

func serve(socket net.Conn, token [32]byte, build string) error {
	return serveScoped(socket, token, build, nil)
}

func serveScoped(socket net.Conn, token [32]byte, build string, qualification *inputQualification) (result error) {
	if err := socket.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	var hello Request
	if err := ReadMessage(socket, &hello); err != nil {
		return err
	}
	if hello.Version != ProtocolVersion || hello.Build != build || hello.Operation != "hello" || subtle.ConstantTimeCompare(hello.Token, token[:]) != 1 {
		return ErrProtocol
	}
	if qualification != nil && (!hello.AppControl || !hello.Media) {
		return ErrProtocol
	}
	captures := newCaptureHost(hello.Media, token)
	catalog := make(sourceCatalog)
	var apps *appHost
	if hello.AppControl {
		if !hello.Media {
			return ErrProtocol
		}
		connection, err := openPrivateParent(6, token)
		if err != nil {
			return err
		}
		// Authenticate media before app observations can race capture setup.
		captures.media, err = openMediaParent(token)
		if err != nil {
			connection.Close()
			return err
		}
		apps = newAppHostScoped(connection, build, "", captures, qualification)
	}
	clear(token[:])
	clear(hello.Token)
	if err := socket.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	nativeEpoch := newEpoch()
	if apps != nil {
		apps.epoch = nativeEpoch
		go apps.serve()
	}
	resources := make(map[protocol.ResourceKey]resourceDisplay)
	defer func() {
		appErr := apps.stop()
		result = errors.Join(result, appErr)
		captureErr := captures.close()
		result = errors.Join(result, captureErr)
		if captureErr != nil || appErr != nil {
			return
		}
		for _, resource := range resources {
			if resource.display.ID == 0 {
				continue
			}
			cleanupErr := disposeDisplay(resource.display.ID)
			result = errors.Join(result, cleanupErr)
			if cleanupErr == nil && resource.claim != nil {
				result = errors.Join(result, resource.claim.ReleaseAfterQuiescence())
			}
		}
	}()
	if err := WriteMessage(socket, Response{Version: ProtocolVersion, Build: build, ID: hello.ID, Epoch: protocol.VscreenEpoch{NativeEpoch: nativeEpoch}}); err != nil {
		return err
	}
	for {
		var request Request
		if err := ReadMessage(socket, &request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		response := Response{Version: ProtocolVersion, Build: build, ID: request.ID, Epoch: protocol.VscreenEpoch{NativeEpoch: nativeEpoch}}
		if qualification != nil && qualification.checkControl(request) != nil {
			return ErrProtocol
		}
		if request.Version != ProtocolVersion || request.Build != build || request.ID == "" || len(request.Token) > 0 || request.Media || request.AppControl || request.App != nil {
			return ErrProtocol
		}
		if request.Operation == "update_exclusions" {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := captures.updateExclusions(ctx, request.ExcludedWindowIDs); err != nil {
				response.Error = "capture_update_failed"
			}
			cancel()
		} else if request.Operation == "list" {
			resources := capture.LiveResources()
			response.LiveResources = &resources
			displays, err := listDisplays()
			response.Displays = displays
			if err != nil {
				response.Error = err.Error()
			}
		} else if request.Operation == "sources" || request.Operation == "start_capture" || request.Operation == "stop_capture" || request.Operation == "force_keyframe" || request.Operation == "capture_status" {
			response = captureResponse(captures, catalog, resources, nativeEpoch, request, response)
		} else {
			if apps != nil && (request.Operation == "dispose" || request.Operation == "quiesce") {
				if err := apps.lifecycle(request); err != nil {
					response.Error = appError(err)
				}
			}
			if request.Operation == "dispose" {
				resource, exists := resources[request.Resource]
				if exists && resource.quiescent && resource.epoch == request.Epoch {
					if err := captures.stopResource(request.Resource); err != nil {
						response.Error = err.Error()
					}
				}
			}
			if response.Error == "" {
				response = handleRequest(resources, nativeEpoch, request, response)
			}
		}
		if apps != nil {
			apps.syncRegistry(resources, catalog)
		}
		if err := socket.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		if err := WriteMessage(socket, response); err != nil {
			return err
		}
	}
}

func handleRequest(resources map[protocol.ResourceKey]resourceDisplay, nativeEpoch string, request Request, response Response) Response {
	if err := request.Resource.Validate(); err != nil || request.Resource.UID != uint32(os.Getuid()) {
		response.Error = "resource_invalid"
		return response
	}
	resource, exists := resources[request.Resource]
	switch request.Operation {
	case "ensure":
		if request.Width < 320 || request.Width > 4096 || request.Height < 240 || request.Height > 2160 {
			response.Error = "geometry_invalid"
			return response
		}
		if exists {
			if resource.display.ID == 0 {
				response.Error = "display_unavailable"
				return response
			}
			if request.Width != resource.display.Width || request.Height != resource.display.Height {
				response.Error = "geometry_conflict"
				return response
			}
		} else {
			if len(resources) >= 16 {
				response.Error = "resource_limit"
				return response
			}
			encoded, err := json.Marshal(request.Resource)
			if err != nil {
				response.Error = "resource_invalid"
				return response
			}
			claim, err := acquireRuntimeClaim(request.Resource)
			if err != nil {
				response.Error = "runtime_claim_unavailable"
				return response
			}
			// Keep the claim after uncertain creation; another host must not create over it.
			resources[request.Resource] = resourceDisplay{claim: claim}
			digest := sha256.Sum256(encoded)
			display, err := createDisplay("Multica "+hex.EncodeToString(digest[:6]), binary.BigEndian.Uint32(digest[:4]), request.Width, request.Height)
			if err != nil {
				response.Error = err.Error()
				return response
			}
			resource = resourceDisplay{claim: claim, display: display, epoch: protocol.VscreenEpoch{NativeEpoch: nativeEpoch, DisplayGeneration: newEpoch(), GeometryRevision: 1}}
			resources[request.Resource] = resource
		}
	case "describe", "quiesce", "dispose":
		if !exists {
			response.Error = "display_unavailable"
			return response
		}
		if request.Epoch != resource.epoch {
			response.Error = "stale_epoch"
			return response
		}
		if request.Operation == "quiesce" {
			resource.quiescent = true
			resources[request.Resource] = resource
		}
		if request.Operation == "dispose" {
			if !resource.quiescent {
				response.Error = "quiescence_required"
				return response
			}
			if err := disposeDisplay(resource.display.ID); err != nil {
				response.Error = err.Error()
				return response
			}
			if resource.claim != nil {
				if err := resource.claim.ReleaseAfterQuiescence(); err != nil {
					response.Error = "runtime_claim_release_failed"
					return response
				}
			}
			delete(resources, request.Resource)
			response.Epoch = resource.epoch
			response.Quiescent = true
			return response
		}
	default:
		response.Error = "operation_unsupported"
		return response
	}
	display, err := describeDisplay(resource.display.ID)
	if err != nil || display.UUID != resource.display.UUID {
		response.Error = "display_unavailable"
		return response
	}
	if geometryChanged(resource.display, display) {
		resource.epoch.GeometryRevision++
		resource.display = display
		resource.quiescent = true
		resources[request.Resource] = resource
	}
	response.Display = &display
	response.Epoch = resource.epoch
	response.Quiescent = resource.quiescent
	return response
}

func newEpoch() string { var value [32]byte; rand.Read(value[:]); return hex.EncodeToString(value[:]) }
