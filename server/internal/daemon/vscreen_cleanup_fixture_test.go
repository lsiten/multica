//go:build darwin || linux

package daemon

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenGeometryReadbackCleanup(t *testing.T) {
	for _, operation := range []string{"describe", "quiesce"} {
		t.Run(operation, func(t *testing.T) {
			t.Setenv("VSCREEN_FIXTURE_GEOMETRY_AT", operation)
			d := vscreenFixtureDaemon(t)
			g, ctx, cancel := d.beginMirrorControlConnection(context.Background())
			defer cancel()
			d.vscreenServerGeneration = "server"
			command := func(runtime string, kind protocol.VscreenCommandKind) error {
				return d.executeVscreenCommand(ctx, protocol.VscreenCommand{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: runtime, DaemonGeneration: "server", RequestID: "cleanup"}, CommandID: string(kind) + runtime, Kind: kind}, g)
			}
			for _, runtime := range []string{"rt", "other"} {
				if err := command(runtime, protocol.VscreenCommandEnable); err != nil {
					t.Fatal(err)
				}
			}
			state, err := d.vscreenSnapshot(ctx, "ws", "rt")
			if err != nil {
				t.Fatal(err)
			}
			if operation == "describe" && state.GeometryRevision != 2 {
				t.Fatalf("geometry = %d", state.GeometryRevision)
			}
			key, err := d.vscreenResource("ws", "rt")
			if err != nil {
				t.Fatal(err)
			}
			if operation == "describe" {
				actor, err := d.vscreen.manager.For(key)
				if err != nil {
					t.Fatal(err)
				}
				if err := actor.Recover(ctx, protocol.VscreenEpoch{NativeEpoch: state.NativeEpoch, DisplayGeneration: state.DisplayGeneration, GeometryRevision: state.GeometryRevision}); err != nil {
					t.Fatal(err)
				}
				if actor.Status().Frozen {
					t.Fatal("recovered actor remains frozen")
				}
			}
			if err := command("rt", protocol.VscreenCommandDisable); err != nil {
				t.Fatal(err)
			}
			d.vscreen.driver.mu.Lock()
			_, retained := d.vscreen.driver.displays[key]
			d.vscreen.driver.mu.Unlock()
			if retained {
				t.Fatal("disabled runtime remains in cleanup cache")
			}
			sources, err := d.vscreenSources(ctx, "ws", "rt")
			if err != nil || len(sources) != 1 {
				t.Fatalf("disabled sources=%v err=%v", sources, err)
			}
			sibling, err := d.vscreenSnapshot(ctx, "ws", "other")
			if err != nil || sibling.GeometryRevision != 1 || sibling.State == protocol.VscreenStateDisabled {
				t.Fatalf("sibling=%+v err=%v", sibling, err)
			}
			t.Logf("%s geometry advancement: selected runtime disposed; sibling retained at geometry 1", operation)
		})
	}
}
