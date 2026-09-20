package daemon

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMirrorOfferResourceRequiresExactRuntimeAndDisplay(t *testing.T) {
	base := protocol.ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "workspace", RuntimeID: "runtime", UID: 501}
	for _, kind := range []protocol.MirrorSourceKind{protocol.MirrorSourceVirtual, protocol.MirrorSourcePhysical, protocol.MirrorSourceSystem} {
		t.Run(string(kind), func(t *testing.T) {
			resource := base
			if kind != protocol.MirrorSourceVirtual {
				resource.DisplayID = 7
			}
			source := native.SourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: resource, Source: protocol.MirrorSource{Kind: kind, SourceID: "display:seven"}}, DisplayID: 7}
			grant := protocol.MirrorViewerGrant{Source: source.Source}
			if got, ok := mirrorOfferResource(base, []native.SourceDescriptor{source}, grant); !ok || got != resource {
				t.Fatal("selected display lost its resource identity")
			}
			for _, change := range []string{"workspace", "runtime", "backend", "uid", "display", "source"} {
				t.Run(change, func(t *testing.T) {
					bad := source
					switch change {
					case "workspace":
						bad.Resource.WorkspaceID = "foreign"
					case "runtime":
						bad.Resource.RuntimeID = "foreign"
					case "backend":
						bad.Resource.BackendIdentity = "https://foreign.invalid"
					case "uid":
						bad.Resource.UID++
					case "display":
						bad.Resource.DisplayID++
					case "source":
						bad.Source.SourceID = "display:other"
					}
					if _, ok := mirrorOfferResource(base, []native.SourceDescriptor{bad}, grant); ok {
						t.Fatal("foreign source accepted")
					}
				})
			}
		})
	}
}
