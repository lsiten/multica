package hostclient

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestSourceCatalogAcceptsDisplayScopedResources(t *testing.T) {
	key := protocol.ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "workspace", RuntimeID: "runtime", UID: 501}
	for _, kind := range []protocol.MirrorSourceKind{protocol.MirrorSourceVirtual, protocol.MirrorSourcePhysical, protocol.MirrorSourceSystem} {
		t.Run(string(kind), func(t *testing.T) {
			// Given a catalog entry with the native host's display resource binding.
			resource := key
			if kind != protocol.MirrorSourceVirtual {
				resource.DisplayID = 7
			}
			source := native.SourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: resource, Source: protocol.MirrorSource{Kind: kind, SourceID: "display:seven"}, NativeEpoch: "native", Generation: "generation"}, DisplayID: 7, Width: 1280, Height: 720, LogicalWidth: 1280, LogicalHeight: 720, Scale: 1, GeometryRevision: 1}
			client := &Client{epoch: "native"}
			// When the runtime catalog is validated.
			err := client.validateMediaResponse(native.Request{Operation: "sources", Resource: key}, native.Response{Sources: []native.SourceDescriptor{source}})
			// Then physical/system resources remain distinct without rejecting the catalog.
			if err != nil {
				t.Fatalf("valid %s source rejected: %v", kind, err)
			}
			for _, mutation := range []string{"runtime", "workspace", "backend", "uid", "display"} {
				t.Run(mutation, func(t *testing.T) {
					bad := source
					switch mutation {
					case "runtime":
						bad.Resource.RuntimeID = "foreign"
					case "workspace":
						bad.Resource.WorkspaceID = "foreign"
					case "backend":
						bad.Resource.BackendIdentity = "https://foreign.invalid"
					case "uid":
						bad.Resource.UID++
					case "display":
						bad.Resource.DisplayID++
					}
					if client.validateMediaResponse(native.Request{Operation: "sources", Resource: key}, native.Response{Sources: []native.SourceDescriptor{bad}}) == nil {
						t.Fatal("foreign source accepted")
					}
				})
			}
		})
	}
}
