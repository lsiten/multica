//go:build darwin

package nativecapture

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// RunHost serves one inherited, authenticated, read-only capture socket.
func RunHost(build string) error {
	if !supported() {
		return ErrUnsupported
	}
	if err := prepareDescriptors(); err != nil {
		return err
	}
	socket := os.NewFile(3, "capture-parent")
	if socket == nil {
		return ErrProtocol
	}
	defer socket.Close()
	bootstrap := os.NewFile(4, "capture-bootstrap")
	if bootstrap == nil {
		return ErrProtocol
	}
	defer bootstrap.Close()
	if err := validateParent(socket, bootstrap); err != nil {
		return err
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
	go func() {
		done <- serve(connection, token, build)
		stopLoop()
	}()
	runLoop()
	return <-done
}

func serve(socket net.Conn, token [32]byte, build string) (result error) {
	if err := socket.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	var hello Request
	if err := ReadMessage(socket, &hello); err != nil {
		return err
	}
	if hello.Version != ProtocolVersion || hello.Build != build || hello.Operation != "hello" ||
		!hello.Media || subtle.ConstantTimeCompare(hello.Token, token[:]) != 1 {
		return ErrProtocol
	}
	host := newCaptureHost(token)
	defer func() { result = errors.Join(result, host.close()) }()
	clear(token[:])
	clear(hello.Token)
	if err := socket.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	nativeEpoch := newEpoch()
	if err := WriteMessage(socket, Response{Version: ProtocolVersion, Build: build, ID: hello.ID, Epoch: Epoch{NativeEpoch: nativeEpoch}}); err != nil {
		return err
	}
	catalog := make(sourceCatalog)
	for {
		var request Request
		if err := ReadMessage(socket, &request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if request.Version != ProtocolVersion || request.Build != build || request.ID == "" ||
			len(request.Token) > 0 || request.Media {
			return ErrProtocol
		}
		response := Response{Version: ProtocolVersion, Build: build, ID: request.ID, Epoch: Epoch{NativeEpoch: nativeEpoch}}
		response = handleCaptureRequest(host, catalog, nativeEpoch, request, response)
		if err := socket.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		if err := WriteMessage(socket, response); err != nil {
			return err
		}
	}
}

func handleCaptureRequest(host *captureHost, catalog sourceCatalog, nativeEpoch string, request Request, response Response) Response {
	if request.Resource.Validate() != nil || request.Resource.UID != uint32(os.Getuid()) {
		response.Error = "resource_invalid"
		return response
	}
	displays, err := listDisplays()
	if err != nil {
		response.Error = err.Error()
		return response
	}
	catalog.refresh(displays, nativeEpoch)
	sources, err := catalog.project(request.Resource)
	if err != nil {
		response.Error = err.Error()
		return response
	}
	var descriptor *CaptureDescriptor
	switch request.Operation {
	case "sources":
		response.Sources = sources
		return response
	case "start_capture":
		descriptor, err = host.start(request, sources)
	case "stop_capture", "force_keyframe", "capture_status":
		descriptor, err = host.operate(request)
	default:
		err = ErrProtocol
	}
	if err != nil {
		response.Error = err.Error()
		return response
	}
	response.Capture = descriptor
	response.Epoch = Epoch{
		NativeEpoch:       descriptor.Source.NativeEpoch,
		DisplayGeneration: descriptor.Source.Generation,
		GeometryRevision:  descriptor.Source.GeometryRevision,
	}
	return response
}

func validateParent(socket, bootstrap *os.File) error {
	info, err := bootstrap.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		return ErrProtocol
	}
	credentials, err := unix.GetsockoptXucred(int(socket.Fd()), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return err
	}
	if credentials.Uid != uint32(os.Getuid()) {
		return ErrProtocol
	}
	return nil
}

func prepareDescriptors() error { return unix.SetNonblock(4, true) }
