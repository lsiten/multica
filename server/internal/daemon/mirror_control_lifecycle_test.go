//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native/globalinput"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

type releaseObservingInjector struct {
	physicalInjectorFixture
	released chan string
}

func (f *releaseObservingInjector) Pointer(event globalinput.PointerEvent) error {
	if err := f.physicalInjectorFixture.Pointer(event); err != nil {
		return err
	}
	if event.Kind == protocol.MirrorInputPointerUp {
		f.released <- "pointer"
	}
	return nil
}
func (f *releaseObservingInjector) Key(event globalinput.KeyEvent) error {
	if err := f.physicalInjectorFixture.Key(event); err != nil {
		return err
	}
	if !event.Down {
		f.released <- "key"
	}
	return nil
}

func TestMirrorControlChannelReleasesNativeInput(t *testing.T) {
	for _, kind := range []string{"pointer", "key"} {
		for _, loss := range []string{"revoke", "peer", "channel", "expiry", "emergency", "timeout", "continuing", "normal_up"} {
			t.Run(kind+"/"+loss, func(t *testing.T) {
				d := vscreenFixtureDaemon(t)
				prior := d.globalInjector()
				fixture := &releaseObservingInjector{physicalInjectorFixture: physicalInjectorFixture{available: true}, released: make(chan string, 8)}
				globalInjectorInst = fixture
				t.Cleanup(func() { globalInjectorInst = prior })
				sources, err := d.vscreenSources(t.Context(), "ws", "rt")
				if err != nil {
					t.Fatal(err)
				}
				source := sources[0]
				backend := newMirrorControlBackend(d)
				backend.remember("rt", sources)
				d.SetHumanInteractionEnabled(true)
				arbiter := mirror.NewArbiter()
				rm := mirror.NewRuntimeMirror(nil, time.Hour)
				rm.SetControlBackend(backend)
				rm.SetArbiter(arbiter)
				t.Cleanup(func() {
					if err := rm.Close(context.Background()); err != nil {
						t.Error(err)
					}
				})
				pc, input, acks := connectInputFixture(t, rm)
				expires := time.Now().Add(time.Minute)
				if loss == "expiry" {
					expires = time.Now().Add(200 * time.Millisecond)
				}
				grant := protocol.MirrorControlGrant{GrantID: "control", ViewerID: "viewer", UserID: "user", SessionID: "session", WorkspaceID: "ws", RuntimeID: "rt", Source: source.Source, NativeEpoch: source.NativeEpoch, SourceGeneration: source.Generation, ExpiresAt: expires}
				if !rm.BindControlGrant("viewer", grant, 1) {
					t.Fatal("bind control")
				}
				msg := protocol.MirrorInputMessage{GrantID: grant.GrantID, GestureID: "held", Seq: 1, NativeEpoch: source.NativeEpoch, DisplayGeneration: source.Generation, GeometryRevision: source.GeometryRevision}
				if kind == "pointer" {
					msg.Kind = protocol.MirrorInputPointerDown
					msg.Pointer = &protocol.MirrorPointerInput{Button: protocol.MirrorButtonLeft, X: 25, Y: 40}
				} else {
					msg.Kind = protocol.MirrorInputKeyDown
					msg.Key = &protocol.MirrorKeyInput{Key: "a"}
				}
				sendFixtureInput(t, input, acks, msg)
				if loss == "continuing" {
					if kind == "pointer" {
						msg.Kind = protocol.MirrorInputPointerMove
					}
					ticker := time.NewTicker(200 * time.Millisecond)
					defer ticker.Stop()
					for range 24 {
						<-ticker.C
						msg.Seq++
						sendFixtureInput(t, input, acks, msg)
					}
				}
				switch loss {
				case "revoke":
					rm.RevokeControlGrant("viewer", grant.GrantID, 1)
				case "peer":
					if err := pc.Close(); err != nil {
						t.Fatal(err)
					}
				case "channel":
					if err := input.Close(); err != nil {
						t.Fatal(err)
					}
				case "emergency":
					rm.RevokeAllControl(1)
				case "normal_up":
					if kind == "pointer" {
						msg.Kind = protocol.MirrorInputPointerUp
					} else {
						msg.Kind = protocol.MirrorInputKeyUp
					}
					msg.Seq++
					sendFixtureInput(t, input, acks, msg)
				}
				wait := 5 * time.Second
				if loss == "continuing" {
					wait = 700 * time.Millisecond
				}
				select {
				case released := <-fixture.released:
					if released != kind {
						t.Fatalf("released %q, want %q", released, kind)
					}
				case <-time.After(wait):
					t.Fatal("native input stayed down beyond recovery deadline")
				}
				rm.RevokeAllControl(1)
				select {
				case extra := <-fixture.released:
					t.Fatalf("native up duplicated: %s", extra)
				default:
				}
			})
		}
	}
}

func connectInputFixture(t *testing.T, rm *mirror.RuntimeMirror) (*webrtc.PeerConnection, *webrtc.DataChannel, <-chan protocol.MirrorInputAck) {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pc.Close(); err != nil {
			t.Error(err)
		}
	})
	input, err := pc.CreateDataChannel("mirror-input", nil)
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{})
	input.OnOpen(func() { close(opened) })
	acks := make(chan protocol.MirrorInputAck, 16)
	input.OnMessage(func(message webrtc.DataChannelMessage) {
		var ack protocol.MirrorInputAck
		if json.Unmarshal(message.Data, &ack) == nil {
			acks <- ack
		}
	})
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-time.After(5 * time.Second):
		t.Fatal("offer gathering timeout")
	}
	answer, err := rm.Answer(t.Context(), "viewer", mirror.SessionDescriptionFromPion(*pc.LocalDescription()), mirror.ICEConfig{})
	if err != nil {
		t.Fatal(err)
	}
	answer.Commit()
	if err := pc.SetRemoteDescription(answer.SessionDescription.Pion()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("input channel not open")
	}
	return pc, input, acks
}

func sendFixtureInput(t *testing.T, input *webrtc.DataChannel, acks <-chan protocol.MirrorInputAck, msg protocol.MirrorInputMessage) {
	t.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := input.Send(raw); err != nil {
		t.Fatal(err)
	}
	select {
	case ack := <-acks:
		if ack.Type != protocol.MirrorInputAckTypeOK {
			t.Fatalf("input rejected: %+v", ack)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("input acknowledgement timeout")
	}
}
