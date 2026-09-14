package native

import (
	"os"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestSourcesExcludeOtherRuntimeAndOtherHostVirtualDisplays(t *testing.T) {
	// Given: own virtual, another runtime virtual, another host virtual, and a system display.
	key := protocol.ResourceKey{BackendIdentity: "https://fixture.invalid", WorkspaceID: "w", RuntimeID: "a", UID: uint32(os.Getuid())}
	other := key
	other.RuntimeID = "b"
	displays := []Display{{ID: 1, UUID: "own", Managed: true}, {ID: 2, UUID: "other", Managed: true}, {ID: 3, UUID: "other-host", Managed: true}, {ID: 4, UUID: "system", Main: true}}
	resources := map[protocol.ResourceKey]resourceDisplay{key: {display: displays[0], epoch: protocol.VscreenEpoch{NativeEpoch: "native", DisplayGeneration: "own", GeometryRevision: 1}}, other: {display: displays[1]}}
	catalog := make(sourceCatalog)
	catalog.refresh(displays, "native")
	// When
	sources, err := catalog.project(key, resources)
	if err != nil {
		t.Fatal(err)
	}
	// Then: only this runtime's virtual and the system display are authorized.
	if len(sources) != 2 || sources[0].DisplayID != 1 || sources[0].Source.Kind != protocol.MirrorSourceVirtual || sources[1].DisplayID != 4 || sources[1].Source.Kind != protocol.MirrorSourceSystem {
		t.Fatalf("unexpected sources %+v", sources)
	}
	for _, source := range sources {
		if source.Resource != key {
			t.Fatal("foreign binding")
		}
	}
}

func TestCaptureWithoutMediaCapabilityDoesNotTouchDescriptorFive(t *testing.T) {
	// Given
	host := newCaptureHost(false, [32]byte{})
	// When
	descriptor, err := host.start(Request{}, nil)
	// Then
	if descriptor != nil || err == nil || err.Error() != "media_unavailable" || host.media != nil {
		t.Fatalf("descriptor=%v err=%v", descriptor, err)
	}
}

func TestCaptureRejectsStaleSourceBeforeNativeAccess(t *testing.T) {
	// Given
	host := newCaptureHost(true, [32]byte{})
	source := SourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Source: protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "physical"}, NativeEpoch: "current", Generation: "current"}, GeometryRevision: 2}
	// When
	_, err := host.start(Request{Capture: &CaptureOptions{StreamID: "00112233445566778899aabbccddeeff", Source: source.Source}, Epoch: protocol.VscreenEpoch{NativeEpoch: "old", DisplayGeneration: "old", GeometryRevision: 1}}, []SourceDescriptor{source})
	// Then
	if err == nil || err.Error() != "stale_epoch" || host.media != nil {
		t.Fatalf("err=%v", err)
	}
}
