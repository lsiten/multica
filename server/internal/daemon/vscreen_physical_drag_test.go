package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/globalinput"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type dragInjectorFixture struct {
	physicalInjectorFixture
	cancel   context.CancelFunc
	failMove bool
}

func (f *dragInjectorFixture) Pointer(event globalinput.PointerEvent) error {
	if f.failMove && event.Kind == protocol.MirrorInputPointerMove {
		return globalinput.ErrUnavailable
	}
	if err := f.physicalInjectorFixture.Pointer(event); err != nil {
		return err
	}
	if event.Kind == protocol.MirrorInputPointerDown && f.cancel != nil {
		f.cancel()
	}
	return nil
}

func TestPhysicalVscreenDragReleasesNativeButton(t *testing.T) {
	for _, scenario := range []string{"cancelled", "move_failed", "completed"} {
		t.Run(scenario, func(t *testing.T) {
			resource := protocol.ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "rt", UID: 501, DisplayID: 7}
			descriptor := native.SourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: resource, Source: protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "physical"}, NativeEpoch: "native", Generation: "generation"}, Width: 1000, Height: 500, LogicalWidth: 1000, LogicalHeight: 500}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fixture := &dragInjectorFixture{physicalInjectorFixture: physicalInjectorFixture{available: true}, failMove: scenario == "move_failed"}
			if scenario == "cancelled" {
				fixture.cancel = cancel
			}
			execution := newPhysicalVscreenExecution(t.Context(), Task{ID: "task", WorkspaceID: "ws", RuntimeID: "rt", MirrorSource: &descriptor.MirrorSourceBinding}, descriptor, nil, fixture, mirror.NewArbiter(), nil)
			t.Cleanup(execution.Close)
			acquired, err := execution.invoke(ctx, "vscreen_acquire", mustJSON(t, vscreenToolArgs{RequestID: "request"}))
			if err != nil {
				t.Fatal(err)
			}
			var lease struct {
				TransactionID string `json:"transaction_id"`
			}
			if err := json.Unmarshal([]byte(acquired[0]["text"].(string)), &lease); err != nil {
				t.Fatal(err)
			}
			args := vscreenToolArgs{TransactionID: lease.TransactionID, ActionID: "drag", Sequence: 1, Action: &protocol.VscreenAction{Kind: protocol.VscreenActionDrag, Drag: &protocol.VscreenDragAction{From: protocol.VscreenPoint{X: 10, Y: 20}, To: protocol.VscreenPoint{X: 100, Y: 200}, DurationMS: 1}}}
			_, err = execution.invoke(ctx, "vscreen_drag", mustJSON(t, args))
			switch scenario {
			case "cancelled":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel error = %v", err)
				}
			case "move_failed":
				if !errors.Is(err, globalinput.ErrUnavailable) {
					t.Fatalf("move error = %v", err)
				}
			case "completed":
				if err != nil {
					t.Fatal(err)
				}
			}
			want := 2
			if scenario == "completed" {
				want = 3
			}
			if len(fixture.pointers) != want || fixture.pointers[want-1].Kind != protocol.MirrorInputPointerUp {
				t.Fatalf("drag left native button down: %+v", fixture.pointers)
			}
			last := fixture.pointers[want-1]
			if scenario == "completed" && (last.X != 100 || last.Y != 200) {
				t.Fatalf("release location = %+v", last)
			}
			if scenario != "completed" && (last.X != 10 || last.Y != 20) {
				t.Fatalf("failed drag moved pointer during release: %+v", last)
			}
		})
	}
}
