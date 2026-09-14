package native

import (
	"errors"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"net"
	"os"
	"testing"
)

func TestHostRejectsUnauthenticatedParent(t *testing.T) {
	// Given
	parent, child := net.Pipe()
	defer parent.Close()
	defer child.Close()
	done := make(chan error, 1)
	go func() { done <- serve(child, [32]byte{1}, "test") }()
	// When
	if err := WriteMessage(parent, Request{Version: 1, Build: "test", Operation: "hello", Token: make([]byte, 32)}); err != nil {
		t.Fatal(err)
	}
	// Then
	if err := <-done; !errors.Is(err, ErrProtocol) {
		t.Fatalf("got %v", err)
	}
}

func TestHostRejectsStaleEpochBeforeNativeDispatch(t *testing.T) {
	// Given
	key := protocol.ResourceKey{BackendIdentity: "https://fixture.invalid", WorkspaceID: "workspace", RuntimeID: "runtime", UID: uint32(os.Getuid())}
	epoch := protocol.VscreenEpoch{NativeEpoch: "native", DisplayGeneration: "display", GeometryRevision: 1}
	resources := map[protocol.ResourceKey]resourceDisplay{key: {epoch: epoch}}
	// When
	response := handleRequest(resources, "native", Request{Operation: "dispose", Resource: key, Epoch: protocol.VscreenEpoch{NativeEpoch: "stale"}}, Response{})
	// Then
	if response.Error != "stale_epoch" || len(resources) != 1 {
		t.Fatalf("got %+v", response)
	}
}

func TestHostRequiresQuiescenceBeforeDispose(t *testing.T) {
	// Given
	key := protocol.ResourceKey{BackendIdentity: "https://fixture.invalid", WorkspaceID: "workspace", RuntimeID: "runtime", UID: uint32(os.Getuid())}
	epoch := protocol.VscreenEpoch{NativeEpoch: "native", DisplayGeneration: "display", GeometryRevision: 1}
	resources := map[protocol.ResourceKey]resourceDisplay{key: {epoch: epoch}}
	// When
	response := handleRequest(resources, "native", Request{Operation: "dispose", Resource: key, Epoch: epoch}, Response{})
	// Then
	if response.Error != "quiescence_required" || len(resources) != 1 {
		t.Fatalf("got %+v", response)
	}
}
