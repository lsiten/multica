package protocol

import (
	"math"
	"testing"
)

func TestVscreenSourceCatalogRejectsAmbiguousBindings(t *testing.T) {
	source := VscreenSourceDescriptor{MirrorSourceBinding: MirrorSourceBinding{Resource: ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "rt", UID: 501}, Source: MirrorSource{Kind: MirrorSourceVirtual, SourceID: "display"}, NativeEpoch: "native", Generation: "generation"}, Scale: 1}
	for _, name := range []string{"duplicate", "nan", "infinity", "foreign_runtime", "mixed_resource"} {
		t.Run(name, func(t *testing.T) {
			// Given
			result := VscreenQueryResult{VscreenEnvelope: VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "connection", RequestID: "request"}, Sources: []VscreenSourceDescriptor{source}}
			switch name {
			case "duplicate":
				result.Sources = append(result.Sources, source)
			case "nan":
				result.Sources[0].Scale = math.NaN()
			case "infinity":
				result.Sources[0].Scale = math.Inf(1)
			case "foreign_runtime":
				result.Sources[0].Resource.RuntimeID = "foreign"
			case "mixed_resource":
				other := source
				other.Source.SourceID = "other"
				other.Resource.UID = 502
				result.Sources = append(result.Sources, other)
			}
			// When / Then
			if err := result.Validate("sources"); err == nil {
				t.Fatal("ambiguous catalog accepted")
			}
		})
	}
}
