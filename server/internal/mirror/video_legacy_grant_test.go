package mirror

import (
	"context"
	"image"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestLegacyDataChannelGrantExpiresAndRevokes(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "expiry", true: "revoke"}[revoke], func(t *testing.T) {
			m := NewRuntimeMirror(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 4, 4))}, 10*time.Millisecond)
			defer m.Close(context.Background())
			browser, err := webrtc.NewPeerConnection(webrtc.Configuration{})
			if err != nil {
				t.Fatal(err)
			}
			defer browser.Close()
			dc, err := browser.CreateDataChannel("mirror", nil)
			if err != nil {
				t.Fatal(err)
			}
			received := make(chan struct{}, 1)
			dc.OnMessage(func(message webrtc.DataChannelMessage) {
				if len(message.Data) > 0 {
					select {
					case received <- struct{}{}:
					default:
					}
				}
			})
			offer, err := browser.CreateOffer(nil)
			if err != nil {
				t.Fatal(err)
			}
			gather := webrtc.GatheringCompletePromise(browser)
			if err := browser.SetLocalDescription(offer); err != nil {
				t.Fatal(err)
			}
			<-gather
			n, err := m.Answer(context.Background(), "viewer", SessionDescriptionFromPion(*browser.LocalDescription()), ICEConfig{})
			if err != nil {
				t.Fatal(err)
			}
			grant := fixtureGrant(fixtureSource())
			grant.ExpiresAt = time.Now().Add(400 * time.Millisecond)
			if !m.BindViewerGrant("viewer", grant, 2) {
				t.Fatal("legacy grant rejected")
			}
			n.Commit()
			if err := browser.SetRemoteDescription(n.Pion()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-received:
			case <-time.After(time.Second):
				t.Fatal("legacy real datachannel received no JPEG")
			}
			if revoke {
				if m.RevokeViewerGrant("viewer", grant.GrantID, 1) {
					t.Fatal("stale control revoked legacy peer")
				}
				if !m.RevokeViewerGrant("viewer", grant.GrantID, 2) {
					t.Fatal("legacy revoke rejected")
				}
			}
			select {
			case <-n.peer.done:
			case <-time.After(time.Second):
				t.Fatal("legacy authorized peer not stopped")
			}
			if m.HasViewers() {
				t.Fatal("legacy grant termination retained capture")
			}
			t.Log("real legacy JPEG DataChannel delivered media and then grant termination stopped peer/capture")
		})
	}
}

func TestGrantClosesTransportBeforeSlowNativeDetach(t *testing.T) {
	m := NewRuntimeMirror(nil, time.Hour)
	defer m.Close(context.Background())
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	done := make(chan struct{})
	peer := &mirrorPeer{pc: pc, done: make(chan struct{}), grant: &viewerGrant{value: fixtureGrant(fixtureSource()), generation: 1}, detach: func() { <-release }}
	m.peers["viewer"] = peer
	go func() { defer close(done); m.RevokeViewerGrant("viewer", "grant", 1) }()
	deadline := time.NewTimer(100 * time.Millisecond)
	ticker := time.NewTicker(time.Millisecond)
	closed := false
	for !closed {
		select {
		case <-deadline.C:
			goto finish
		case <-ticker.C:
			closed = pc.ConnectionState() == webrtc.PeerConnectionStateClosed
		}
	}
finish:
	deadline.Stop()
	ticker.Stop()
	close(release)
	<-done
	if !closed {
		t.Fatal("revoked transport waited for slow native detach")
	}
}
