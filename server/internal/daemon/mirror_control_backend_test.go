//go:build darwin || linux

package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMirrorControlRefreshesMismatchedCachedSource(t *testing.T) {
	for _, mismatch := range []string{"generation", "geometry"} {
		t.Run(mismatch, func(t *testing.T) {
			sourceLog := filepath.Join(t.TempDir(), "sources.log")
			t.Setenv("MIRROR_FIXTURE_SOURCE_LOG", sourceLog)
			d := vscreenFixtureDaemon(t)
			previous := d.globalInjector()
			fixture := &physicalInjectorFixture{available: true}
			globalInjectorInst = fixture
			t.Cleanup(func() { globalInjectorInst = previous })
			sources, err := d.vscreenSources(t.Context(), "ws", "rt")
			if err != nil {
				t.Fatal(err)
			}
			source := sources[0]
			grant := protocol.MirrorControlGrant{GrantID: "control", ViewerID: "viewer", WorkspaceID: "ws", RuntimeID: "rt", Source: source.Source, NativeEpoch: source.NativeEpoch, SourceGeneration: source.Generation, ExpiresAt: time.Now().Add(time.Minute)}
			old := source
			if mismatch == "generation" {
				old.Generation = "old"
			} else {
				old.GeometryRevision = 2
				old.X = 800
			}
			backend := newMirrorControlBackend(d)
			backend.remember("rt", []native.SourceDescriptor{old})
			msg := protocol.MirrorInputMessage{Kind: protocol.MirrorInputPointerDown, GestureID: "gesture", NativeEpoch: source.NativeEpoch, GeometryRevision: source.GeometryRevision, Pointer: &protocol.MirrorPointerInput{X: 100, Y: 50, Button: protocol.MirrorButtonLeft}}
			if _, ok := backend.ResourceForInput(grant, msg); !ok {
				t.Fatal("fresh source resolution failed before TTL expiry")
			}
			if reason := backend.DispatchInput(t.Context(), source.Resource, grant, msg); reason != "" {
				t.Fatalf("fresh source rejected before TTL expiry: %s", reason)
			}
			msg.Kind = protocol.MirrorInputPointerMove
			for range 4 {
				if reason := backend.DispatchInput(t.Context(), source.Resource, grant, msg); reason != "" {
					t.Fatal(reason)
				}
			}
			calls, err := os.ReadFile(sourceLog)
			if err != nil {
				t.Fatal(err)
			}
			if string(calls) != strings.Repeat("sources\n", 2) {
				t.Fatalf("catalog queries after four moves = %q", calls)
			}
			t.Log("initial catalog + one mismatch refresh; four pointer moves made zero host queries")
			backend.ReleaseInput(grant, msg.GestureID)
			if len(fixture.pointers) != 6 || fixture.pointers[0].X != 100 || fixture.pointers[5].Kind != protocol.MirrorInputPointerUp {
				t.Fatalf("native posts = %+v", fixture.pointers)
			}
			if reason := backend.DispatchInput(t.Context(), source.Resource, grant, msg); reason != protocol.MirrorInputStale {
				t.Fatalf("drag continued after release: %s", reason)
			}
			msg.GeometryRevision = 2
			if reason := backend.DispatchInput(t.Context(), source.Resource, grant, msg); reason != protocol.MirrorInputStale {
				t.Fatalf("old geometry accepted: %s", reason)
			}
		})
	}
}

func TestMirrorControlNativeReleasePreservesOtherOwners(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	previous := d.globalInjector()
	fixture := &physicalInjectorFixture{available: true}
	globalInjectorInst = fixture
	t.Cleanup(func() { globalInjectorInst = previous })
	sources, err := d.vscreenSources(t.Context(), "ws", "rt")
	if err != nil {
		t.Fatal(err)
	}
	source := sources[0]
	grant := protocol.MirrorControlGrant{GrantID: "first", ViewerID: "viewer", WorkspaceID: "ws", RuntimeID: "rt", Source: source.Source, NativeEpoch: source.NativeEpoch, SourceGeneration: source.Generation}
	backend := newMirrorControlBackend(d)
	backend.remember("rt", sources)
	msg := protocol.MirrorInputMessage{Kind: protocol.MirrorInputKeyDown, GestureID: "held", NativeEpoch: source.NativeEpoch, GeometryRevision: source.GeometryRevision, Key: &protocol.MirrorKeyInput{Key: "a"}}
	if reason := backend.DispatchInput(t.Context(), source.Resource, grant, msg); reason != "" {
		t.Fatal(reason)
	}
	other := grant
	other.GrantID = "second"
	msg.Key.Key = "b"
	if reason := backend.DispatchInput(t.Context(), source.Resource, other, msg); reason != "" {
		t.Fatal(reason)
	}
	backend.ReleaseInput(grant, "held")
	backend.ReleaseInput(grant, "held")
	if len(fixture.keys) != 3 || fixture.keys[2].Key != "a" || fixture.keys[2].Down {
		t.Fatalf("native keys = %+v", fixture.keys)
	}
	backend.ReleaseInput(other, "held")
	if len(fixture.keys) != 4 || fixture.keys[3].Key != "b" || fixture.keys[3].Down {
		t.Fatalf("native keys = %+v", fixture.keys)
	}
}

func TestMirrorControlPhysicalCatalogProjectionsShareArbitration(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	backend := newMirrorControlBackend(d)
	arbiter := mirror.NewArbiter()
	for index, runtimeID := range []string{"rt", "other"} {
		sources, err := d.vscreenSources(t.Context(), "ws", runtimeID)
		if err != nil {
			t.Fatal(err)
		}
		source := sources[0]
		backend.remember(runtimeID, sources)
		grant := protocol.MirrorControlGrant{WorkspaceID: "ws", RuntimeID: runtimeID, Source: source.Source, NativeEpoch: source.NativeEpoch, SourceGeneration: source.Generation}
		resource, ok := backend.ResourceForInput(grant, protocol.MirrorInputMessage{GeometryRevision: source.GeometryRevision})
		if !ok {
			t.Fatal("physical source unresolved")
		}
		got := arbiter.Acquire(resource, mirror.Principal{Kind: mirror.PrincipalHuman, ID: runtimeID}, "gesture", time.Second)
		want := mirror.AcquireHeld
		if index == 1 {
			want = mirror.AcquireBusy
		}
		if got != want {
			t.Fatalf("physical display %d projected into %s: %s, want %s", source.DisplayID, runtimeID, got, want)
		}
	}
}
