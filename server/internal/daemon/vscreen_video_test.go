//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

func TestVscreenManagedVideoGrantRevokeKeepsDisplayAndObserver(t *testing.T) {
	for index, name := range []string{"physical", "virtual"} {
		t.Run(name, func(t *testing.T) { testVscreenManagedVideoGrantRevoke(t, index) })
	}
}

func testVscreenManagedVideoGrantRevoke(t *testing.T, sourceIndex int) {
	t.Helper()
	d := vscreenFixtureDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	g, _, stop := d.beginMirrorControlConnection(ctx)
	defer stop()
	d.vscreenServerGeneration = "server"
	e := protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "server", RequestID: "enable"}
	if err := d.executeVscreenCommand(ctx, protocol.VscreenCommand{VscreenEnvelope: e, CommandID: "enable", Kind: protocol.VscreenCommandEnable}, g); err != nil {
		t.Fatal(err)
	}
	catalog, err := d.vscreenSources(ctx, "ws", "rt")
	if err != nil {
		t.Fatal(err)
	}
	source := catalog[sourceIndex]
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if _, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	if _, err = pc.CreateDataChannel("mirror-control", nil); err != nil {
		t.Fatal(err)
	}
	packets := make(chan int, 1)
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		packet, _, err := track.ReadRTP()
		if err == nil {
			packets <- len(packet.Payload)
		}
	})
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gather
	grant := protocol.MirrorViewerGrant{GrantID: "grant", SessionID: "session", WorkspaceID: "ws", RuntimeID: "rt", UserID: "viewer-user", ViewerID: "viewer", NativeEpoch: source.NativeEpoch, Source: source.Source, SourceGeneration: source.Generation, ExpiresAt: time.Now().Add(20 * time.Second)}
	payload := protocol.MirrorOfferPayload{DaemonGeneration: "server", ProtocolVersion: 2, Transport: "video", Source: &source.Source, SourceGeneration: source.Generation, NativeEpoch: source.NativeEpoch, ViewerGrant: &grant, SessionID: grant.SessionID, WorkspaceID: grant.WorkspaceID, RuntimeID: grant.RuntimeID, UserID: grant.UserID, ViewerID: grant.ViewerID, DaemonID: "daemon", ExpiresAt: time.Now().Add(10 * time.Second), Offer: protocol.MirrorSessionDescription{Type: "offer", SDP: pc.LocalDescription().SDP}}
	frames := make(chan protocol.Message, 8)
	msg := mirrorOfferMessage{raw: marshalRaw(payload), controlGeneration: g, enqueue: func(raw []byte) (*wsOutbound, error) {
		var m protocol.Message
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		frames <- m
		return &wsOutbound{}, nil
	}}
	d.handleManagedMirrorOffer(ctx, msg)
	var answer protocol.MirrorAnswerPayload
	select {
	case m := <-frames:
		if m.Type != protocol.EventMirrorAnswer {
			t.Fatalf("answer event %s %s", m.Type, m.Payload)
		}
		if err = json.Unmarshal(m.Payload, &answer); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("answer timeout")
	}
	if answer.VideoQuality == nil || answer.VideoQuality.Validate() != nil {
		t.Fatalf("quality missing: %+v", answer.VideoQuality)
	}
	if err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.Answer.SDP}); err != nil {
		t.Fatal(err)
	}
	select {
	case bytes := <-packets:
		if bytes < 1 {
			t.Fatal("empty RTP")
		}
	case <-ctx.Done():
		t.Fatal("no native fixture RTP")
	}
	s := d.vscreenRuntime()
	q := answer.VideoQuality
	observer, err := s.hub.Subscribe(ctx, mirror.EncodedSource{Binding: source.MirrorSourceBinding, GeometryRevision: source.GeometryRevision, DisplayID: source.DisplayID, Width: q.Width, Height: q.Height, FPS: q.FPS, Bitrate: q.Bitrate, MaxLevelIDC: q.MaxLevelIDC})
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	a, err := d.VscreenActor("ws", "rt")
	if err != nil {
		t.Fatal(err)
	}
	before := a.Status().Display
	revoke := protocol.MirrorViewerRevokePayload{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "server", SessionID: grant.SessionID, ViewerID: grant.ViewerID, GrantID: grant.GrantID}
	d.handleVscreenViewerRevoke(mirrorOfferMessage{raw: marshalRaw(revoke), controlGeneration: g})
	rm, _, ok := d.runtimeMirrorForOffer("rt", g)
	if !ok || rm.HasViewers() {
		t.Fatal("revoked viewer still active")
	}
	if a.Status().Display != before || !a.Status().Ready {
		t.Fatal("viewer revoke changed runtime display")
	}
	if err = observer.ForceKeyframe(); err != nil {
		t.Fatalf("AI observer lost native subscription: %v", err)
	}
	if _, err = observer.Next(ctx); err != nil {
		t.Fatalf("AI observer lost encoded stream: %v", err)
	}
	renewed := grant
	renewed.ExpiresAt = grant.ExpiresAt.Add(time.Second)
	d.handleVscreenViewerRenew(mirrorOfferMessage{raw: marshalRaw(protocol.MirrorViewerRenewPayload{DaemonGeneration: "server", Grant: renewed}), controlGeneration: g})
	if rm.HasViewers() {
		t.Fatal("renew resurrected revoked peer")
	}
	t.Logf("native helper FD5 -> Pion RTP bytes received; quality=%+v; revoked viewer closed; observer still receives IDR; display=%+v", q, before)
}

func TestVscreenRevokeDuringNativeCatalogKeepsHelperAndDisplay(t *testing.T) {
	barrier := filepath.Join(t.TempDir(), "native-catalog")
	t.Setenv("VSCREEN_FIXTURE_SOURCES_BARRIER", barrier)
	d := vscreenFixtureDaemon(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	g, _, stop := d.beginMirrorControlConnection(ctx)
	defer stop()
	d.vscreenServerGeneration = "server"
	envelope := protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "server", RequestID: "enable"}
	if err := d.executeVscreenCommand(ctx, protocol.VscreenCommand{VscreenEnvelope: envelope, CommandID: "enable", Kind: protocol.VscreenCommandEnable}, g); err != nil {
		t.Fatal(err)
	}
	a, err := d.VscreenActor("ws", "rt")
	if err != nil {
		t.Fatal(err)
	}
	display := a.Status().Display
	source := protocol.MirrorSource{Kind: protocol.MirrorSourceVirtual, SourceID: "display:rt"}
	grant := protocol.MirrorViewerGrant{GrantID: "revoked", SessionID: "session", WorkspaceID: "ws", RuntimeID: "rt", UserID: "user", ViewerID: "viewer", NativeEpoch: display.Epoch.NativeEpoch, Source: source, SourceGeneration: display.Epoch.DisplayGeneration, ExpiresAt: time.Now().Add(20 * time.Second)}
	offer := protocol.MirrorOfferPayload{DaemonGeneration: "server", ProtocolVersion: 2, Transport: "video", Source: &source, SourceGeneration: grant.SourceGeneration, NativeEpoch: grant.NativeEpoch, ViewerGrant: &grant, SessionID: grant.SessionID, WorkspaceID: "ws", RuntimeID: "rt", UserID: "user", ViewerID: "viewer", DaemonID: "daemon", Offer: protocol.MirrorSessionDescription{Type: "offer", SDP: "fixture-pending"}, ExpiresAt: time.Now().Add(10 * time.Second)}
	d.handleManagedMirrorOffer(ctx, mirrorOfferMessage{raw: marshalRaw(offer), controlGeneration: g})
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(barrier); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native barrier not reached")
		case <-tick.C:
		}
	}
	d.handleVscreenViewerRevoke(mirrorOfferMessage{raw: marshalRaw(protocol.MirrorViewerRevokePayload{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "server", SessionID: grant.SessionID, ViewerID: grant.ViewerID, GrantID: grant.GrantID}), controlGeneration: g})
	if err := os.WriteFile(barrier+".release", []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { d.vscreenRuntime().grantWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("revoked negotiation did not terminate")
	}
	state, err := d.vscreenSnapshot(ctx, "ws", "rt")
	if err != nil || state.State != protocol.VscreenStateReady || a.Status().Display != display {
		t.Fatalf("viewer cancellation killed shared native helper/display: %+v %v", state, err)
	}
	if rm := d.existingManagedMirror("rt", g); rm != nil && rm.HasViewers() {
		t.Fatal("revoked pending peer survived")
	}
	t.Log("revoke while native source readback in flight: negotiation stopped, shared helper and display stayed alive")
}
