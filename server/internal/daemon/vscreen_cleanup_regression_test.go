package daemon

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenCleanupReadbackRejectsIdentityChanges(t *testing.T) {
	key := protocol.ResourceKey{RuntimeID: "rt"}
	original := vscreen.Display{Resource: key, DisplayID: 2, Epoch: protocol.VscreenEpoch{NativeEpoch: "native", DisplayGeneration: "display", GeometryRevision: 2}}
	for _, mutation := range []string{"resource", "native", "generation", "display", "rollback"} {
		t.Run(mutation, func(t *testing.T) {
			d := &vscreenNativeDriver{displays: map[protocol.ResourceKey]vscreen.Display{key: original}}
			expected := original
			response := native.Response{Epoch: original.Epoch, Display: &native.Display{ID: 2}}
			switch mutation {
			case "resource":
				expected.Resource.RuntimeID = "other"
			case "native":
				response.Epoch.NativeEpoch = "other"
			case "generation":
				response.Epoch.DisplayGeneration = "other"
			case "display":
				response.Display.ID = 3
			case "rollback":
				response.Epoch.GeometryRevision = 1
			}
			if _, err := d.reconcileReadback(expected, response); err == nil {
				t.Fatal("identity change accepted")
			}
			if d.displays[key] != original {
				t.Fatal("rejected readback mutated cleanup identity")
			}
		})
	}
}
