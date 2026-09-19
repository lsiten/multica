package mirror

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

type recordingInputBackend struct {
	inputs []protocol.MirrorInputMessage
}

func (b *recordingInputBackend) InteractionEnabled() bool { return true }
func (b *recordingInputBackend) ResourceForGrant(protocol.MirrorControlGrant) (protocol.ResourceKey, bool) {
	return testResource(1), true
}
func (b *recordingInputBackend) DispatchInput(_ context.Context, _ protocol.ResourceKey, _ protocol.MirrorControlGrant, input protocol.MirrorInputMessage) string {
	b.inputs = append(b.inputs, input)
	return ""
}

func TestInputChannelRejectsReplayAndExpiredCapability(t *testing.T) {
	for _, scenario := range []string{"duplicate", "out_of_order", "expired", "closed", "renewed_replay", "rebound_replay"} {
		t.Run(scenario, func(t *testing.T) {
			m := NewRuntimeMirror(nil, time.Hour)
			t.Cleanup(func() { _ = m.Close(context.Background()) })
			peer := newControlGrantTestPeer(t)
			m.peers["viewer"] = peer
			grant := testControlGrant("control", "display", time.Now().Add(time.Minute))
			if !m.BindControlGrant("viewer", grant, 1) {
				t.Fatal("bind control grant")
			}
			backend := &recordingInputBackend{}
			m.SetControlBackend(backend)
			m.SetArbiter(NewArbiter())
			channel, err := peer.pc.CreateDataChannel("mirror-input", nil)
			if err != nil {
				t.Fatal(err)
			}
			var channelMu sync.Mutex
			closed := false
			input := protocol.MirrorInputMessage{
				Kind: protocol.MirrorInputType, GrantID: grant.GrantID,
				GestureID: "gesture", Seq: 2, NativeEpoch: grant.NativeEpoch,
				DisplayGeneration: grant.SourceGeneration, GeometryRevision: 1,
				Text: &protocol.MirrorTextInput{Text: "hello"},
			}
			send := func() {
				t.Helper()
				payload, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				m.handleInputMessage("viewer", peer, channel, &channelMu, &closed, nil, webrtc.DataChannelMessage{Data: payload})
			}
			send()
			if len(backend.inputs) != 1 {
				t.Fatal("valid input was not dispatched")
			}
			switch scenario {
			case "duplicate":
			case "out_of_order":
				input.Seq = 1
			case "expired":
				peer.mu.Lock()
				peer.controlGrant.deadline = time.Now().Add(-time.Second)
				peer.mu.Unlock()
				input.Seq++
			case "closed":
				peer.mu.Lock()
				peer.closed = true
				peer.mu.Unlock()
				defer func() {
					peer.mu.Lock()
					peer.closed = false
					peer.mu.Unlock()
				}()
				input.Seq++
			case "renewed_replay":
				grant.ExpiresAt = grant.ExpiresAt.Add(time.Second)
				if !m.RenewControlGrant("viewer", grant, 2) {
					t.Fatal("renew control grant")
				}
			case "rebound_replay":
				grant.ExpiresAt = grant.ExpiresAt.Add(time.Second)
				if !m.BindControlGrant("viewer", grant, 2) {
					t.Fatal("rebind control grant")
				}
			}
			send()
			if len(backend.inputs) != 1 {
				t.Fatalf("rejected input reached native backend: %d dispatches", len(backend.inputs))
			}
		})
	}
}

func TestInputChannelWheelReleasesResourceForNextGesture(t *testing.T) {
	m := NewRuntimeMirror(nil, time.Hour)
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	peer := newControlGrantTestPeer(t)
	m.peers["viewer"] = peer
	grant := testControlGrant("control", "display", time.Now().Add(time.Minute))
	if !m.BindControlGrant("viewer", grant, 1) {
		t.Fatal("bind control grant")
	}
	backend := &recordingInputBackend{}
	m.SetControlBackend(backend)
	m.SetArbiter(NewArbiter())
	channel, err := peer.pc.CreateDataChannel("mirror-input", nil)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	closed := false
	for i, gesture := range []string{"first-wheel", "second-wheel"} {
		input := protocol.MirrorInputMessage{
			Kind: protocol.MirrorInputWheel, GrantID: grant.GrantID,
			GestureID: gesture, Seq: uint64(i + 1), NativeEpoch: grant.NativeEpoch,
			DisplayGeneration: grant.SourceGeneration, GeometryRevision: 1,
			Pointer: &protocol.MirrorPointerInput{X: 20, Y: 30, DeltaY: 10},
		}
		payload, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		m.handleInputMessage("viewer", peer, channel, &mu, &closed, nil, webrtc.DataChannelMessage{Data: payload})
	}
	if len(backend.inputs) != 2 {
		t.Fatalf("wheel events dispatched = %d, want 2 without waiting for lock expiry", len(backend.inputs))
	}
	if m.Arbiter().ActiveGestures() != 0 {
		t.Fatal("completed wheel gesture retained the display lock")
	}
}
