package mirror

import (
	"context"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

func fixtureGrant(s EncodedSource) protocol.MirrorViewerGrant {
	return protocol.MirrorViewerGrant{GrantID: "grant", SessionID: "session", WorkspaceID: s.Binding.Resource.WorkspaceID, RuntimeID: s.Binding.Resource.RuntimeID, UserID: "user", ViewerID: "viewer", NativeEpoch: s.Binding.NativeEpoch, Source: s.Binding.Source, SourceGeneration: s.Binding.Generation, ExpiresAt: time.Now().Add(10 * time.Second)}
}
func videoOffer(t *testing.T, kind webrtc.RTPCodecType, onMessage ...func(webrtc.DataChannelMessage)) (*webrtc.PeerConnection, SessionDescription) {
	t.Helper()
	var pc *webrtc.PeerConnection
	var err error
	if kind == webrtc.RTPCodecTypeVideo {
		pc, err = newVideoPeerConnection(ICEConfig{})
	} else {
		pc, err = webrtc.NewPeerConnection(webrtc.Configuration{})
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	if _, err := pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	if len(onMessage) > 0 {
		channel, err := pc.CreateDataChannel("mirror-control", nil)
		if err != nil {
			t.Fatal(err)
		}
		channel.OnMessage(onMessage[0])
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gather:
	case <-time.After(3 * time.Second):
		t.Fatal("ICE gather timeout")
	}
	return pc, SessionDescriptionFromPion(*pc.LocalDescription())
}
func TestVideoRejectsAudioOnlyOffer(t *testing.T) {
	m := NewRuntimeMirror(nil, time.Hour)
	defer m.Close(context.Background())
	m.SetCaptureHub(NewCaptureHub(&fixtureProvider{}))
	_, offer := videoOffer(t, webrtc.RTPCodecTypeAudio)
	source := fixtureSource()
	n, err := m.AnswerVideo(context.Background(), "viewer", offer, ICEConfig{}, source, fixtureGrant(source), 1)
	if err == nil {
		_ = n.Abandon()
		t.Fatal("audio-only offer accepted by video transport")
	}
}

func TestVideoGrantExpiryRenewalAndRevocation(t *testing.T) {
	m := NewRuntimeMirror(nil, time.Hour)
	defer m.Close(context.Background())
	m.SetCaptureHub(NewCaptureHub(&fixtureProvider{}))
	_, offer := videoOffer(t, webrtc.RTPCodecTypeVideo)
	source := fixtureSource()
	grant := fixtureGrant(source)
	grant.ExpiresAt = time.Now().Add(150 * time.Millisecond)
	n, err := m.AnswerVideo(context.Background(), "viewer", offer, ICEConfig{}, source, grant, 2)
	if err != nil {
		t.Fatal(err)
	}
	n.Commit()
	newer := grant
	newer.ExpiresAt = time.Now().Add(time.Second)
	if m.RenewViewerGrant("viewer", newer, 1) {
		t.Fatal("old control generation renewed grant")
	}
	foreign := newer
	foreign.UserID = "other"
	if m.RenewViewerGrant("viewer", foreign, 2) {
		t.Fatal("changed principal renewed grant")
	}
	if !m.RenewViewerGrant("viewer", newer, 2) {
		t.Fatal("valid renewal rejected")
	}
	if m.RenewViewerGrant("viewer", grant, 2) {
		t.Fatal("old expiry accepted")
	}
	if m.RevokeViewerGrant("viewer", "old-grant", 2) || m.RevokeViewerGrant("viewer", grant.GrantID, 1) {
		t.Fatal("stale revoke accepted")
	}
	if !m.RevokeViewerGrant("viewer", grant.GrantID, 2) {
		t.Fatal("grant revoke rejected")
	}
	select {
	case <-n.peer.done:
	case <-time.After(time.Second):
		t.Fatal("revoked peer not closed")
	}
	_, offer2 := videoOffer(t, webrtc.RTPCodecTypeVideo)
	grant.ExpiresAt = time.Now().Add(100 * time.Millisecond)
	next, err := m.AnswerVideo(context.Background(), "viewer", offer2, ICEConfig{}, source, grant, 3)
	if err != nil {
		t.Fatal(err)
	}
	next.Commit()
	select {
	case <-next.peer.done:
	case <-time.After(time.Second):
		t.Fatal("expired grant retained peer")
	}
	t.Log("foreign/old renewal rejected; stale revoke ignored; matching revoke and grant timer close exact peer")
}

func TestVideoCancelledNegotiationDoesNotOpenCapture(t *testing.T) {
	p := &fixtureProvider{}
	m := NewRuntimeMirror(nil, time.Hour)
	defer m.Close(context.Background())
	m.SetCaptureHub(NewCaptureHub(p))
	_, offer := videoOffer(t, webrtc.RTPCodecTypeVideo)
	ctx, cancel := context.WithCancel(context.Background())
	source := fixtureSource()
	n, err := m.AnswerVideo(ctx, "viewer", offer, ICEConfig{}, source, fixtureGrant(source), 1)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	select {
	case <-n.peer.done:
	case <-time.After(time.Second):
		t.Fatal("canceled pre-commit peer remains")
	}
	if len(p.streams) != 0 {
		t.Fatal("unattached negotiation started media")
	}
}
