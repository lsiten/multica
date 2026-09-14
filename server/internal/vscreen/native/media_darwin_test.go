//go:build darwin && cgo && nativeintegration

package native_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/capture"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"golang.org/x/sys/unix"
)

func TestHostMediaPermissionDenial(t *testing.T) {
	// Given: no screen recording consent; the probe will never start a SCStream.
	granted, err := capture.PermissionGranted()
	if err != nil {
		t.Fatal(err)
	}
	if granted {
		t.Fatal("denial fixture requires denied permission; no host started")
	}
	binary, evidence := os.Getenv("MULTICA_VSCREEN_SMOKE_BINARY"), os.Getenv("MULTICA_VSCREEN_SMOKE_EVIDENCE")
	if binary == "" || evidence == "" {
		t.Fatal("explicit binary and evidence paths required")
	}
	pair := func() (net.Conn, *os.File) {
		fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		if err != nil {
			t.Fatal(err)
		}
		unix.CloseOnExec(fds[0])
		unix.CloseOnExec(fds[1])
		parent := os.NewFile(uintptr(fds[0]), "parent")
		child := os.NewFile(uintptr(fds[1]), "child")
		connection, err := net.FileConn(parent)
		if err != nil {
			t.Fatal(err)
		}
		if err := parent.Close(); err != nil {
			t.Fatal(err)
		}
		return connection, child
	}
	control, childControl := pair()
	defer control.Close()
	defer childControl.Close()
	media, childMedia := pair()
	defer media.Close()
	defer childMedia.Close()
	bootstrap, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "internal-vscreen-host")
	command.ExtraFiles = []*os.File{childControl, bootstrap, childMedia}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		control.Close()
		if command.ProcessState == nil {
			if err := command.Wait(); err != nil {
				t.Error(err)
			}
		}
	}()
	if err := childControl.Close(); err != nil {
		t.Fatal(err)
	}
	if err := childMedia.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatal(err)
	}
	var token [32]byte
	rand.Read(token[:])
	if _, err := writer.Write(token[:]); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := control.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := media.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := media.Write(token[:]); err != nil {
		t.Fatal(err)
	}
	exchange := func(request native.Request) native.Response {
		request.Version = 1
		request.Build = "dev/unknown"
		if err := native.WriteMessage(control, request); err != nil {
			t.Fatal(err)
		}
		var response native.Response
		if err := native.ReadMessage(control, &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	hello := exchange(native.Request{ID: "hello", Operation: "hello", Token: token[:], Media: true})
	if hello.Error != "" {
		t.Fatal(hello.Error)
	}
	key := protocol.ResourceKey{BackendIdentity: "https://fixture.invalid", WorkspaceID: "media", RuntimeID: "denial", UID: uint32(os.Getuid())}
	catalog := exchange(native.Request{ID: "sources", Operation: "sources", Resource: key})
	if catalog.Error != "" || len(catalog.Sources) == 0 {
		t.Fatalf("catalog %+v", catalog)
	}
	source := catalog.Sources[0]
	// When: the parent capability authenticates FD 5, then requests an exact catalog source.
	response := exchange(native.Request{ID: "capture", Operation: "start_capture", Resource: key, Epoch: protocol.VscreenEpoch{NativeEpoch: source.NativeEpoch, DisplayGeneration: source.Generation, GeometryRevision: source.GeometryRevision}, Capture: &native.CaptureOptions{StreamID: "00112233445566778899aabbccddeeff", Source: source.Source, Width: 1280, Height: 720, FPS: 30, Bitrate: 4000000, MaxLevelIDC: 31}})
	// Then: permission denial is explicit and the independent control lane remains responsive.
	if response.Error != capture.ErrPermission.Error() || response.Capture != nil {
		t.Fatalf("capture result %+v", response)
	}
	alive := exchange(native.Request{ID: "alive", Operation: "list"})
	if alive.Error != "" || len(alive.Displays) == 0 {
		t.Fatalf("control failed %+v", alive)
	}
	body, err := json.MarshalIndent(struct {
		Sources      []native.SourceDescriptor `json:"sources"`
		Error        string                    `json:"capture_error"`
		ControlAlive bool                      `json:"control_alive"`
	}{catalog.Sources, response.Error, true}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidence, "task-4-native-host-media.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
}
