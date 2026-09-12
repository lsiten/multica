package mirror

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestRuntimeMirrorAnswersBrowserOffer(t *testing.T) {
	// Given
	mirror := NewRuntimeMirror(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	defer func() {
		if err := mirror.Close(context.Background()); err != nil {
			t.Fatalf("close mirror: %v", err)
		}
	}()
	browser, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create browser peer: %v", err)
	}
	defer browser.Close()
	if _, err := browser.CreateDataChannel("mirror", nil); err != nil {
		t.Fatalf("create mirror channel: %v", err)
	}
	offer, err := browser.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(browser)
	if err := browser.SetLocalDescription(offer); err != nil {
		t.Fatalf("set browser offer: %v", err)
	}
	<-gathered

	// When
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	answer, err := mirror.Answer(ctx, "viewer-1", SessionDescriptionFromPion(*browser.LocalDescription()), ICEConfig{})
	if err != nil {
		t.Fatalf("answer offer: %v", err)
	}

	// Then
	if answer.Type != "answer" || answer.SDP == "" {
		t.Fatalf("answer = %+v, want a populated answer", answer)
	}
	if err := browser.SetRemoteDescription(answer.Pion()); err != nil {
		t.Fatalf("set mirror answer: %v", err)
	}
}

func TestRuntimeMirrorKeepsCommittedNegotiationWhenControlContextEnds(t *testing.T) {
	// Given
	runtimeMirror := newRuntimeMirror(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour, time.Hour)
	t.Cleanup(func() { _ = runtimeMirror.Close(context.Background()) })
	browser, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create browser peer: %v", err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	if _, err := browser.CreateDataChannel("mirror", nil); err != nil {
		t.Fatalf("create mirror channel: %v", err)
	}
	offer, err := browser.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(browser)
	if err := browser.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local offer: %v", err)
	}
	<-gathered
	ctx, cancel := context.WithCancel(context.Background())
	negotiation, err := runtimeMirror.Answer(ctx, "viewer-committed", SessionDescriptionFromPion(*browser.LocalDescription()), ICEConfig{})
	if err != nil {
		t.Fatalf("answer offer: %v", err)
	}
	negotiation.Commit()

	// When
	cancel()
	time.Sleep(25 * time.Millisecond)

	// Then
	runtimeMirror.mu.Lock()
	_, exists := runtimeMirror.peers["viewer-committed"]
	runtimeMirror.mu.Unlock()
	if !exists {
		t.Fatal("committed negotiation was closed by control-context cancellation")
	}
}

func TestRuntimeMirrorClosesNegotiationWhenContextEndsBeforeDataChannelAttaches(t *testing.T) {
	// Given
	runtimeMirror := NewRuntimeMirror(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	t.Cleanup(func() { _ = runtimeMirror.Close(context.Background()) })
	browser, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create browser peer: %v", err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	if _, err := browser.CreateDataChannel("mirror", nil); err != nil {
		t.Fatalf("create mirror channel: %v", err)
	}
	offer, err := browser.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(browser)
	if err := browser.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local offer: %v", err)
	}
	<-gathered
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := runtimeMirror.Answer(ctx, "viewer-canceled", SessionDescriptionFromPion(*browser.LocalDescription()), ICEConfig{}); err != nil {
		t.Fatalf("answer offer: %v", err)
	}

	// When: do not deliver the answer to the browser, so the DataChannel cannot attach.
	cancel()

	// Then
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtimeMirror.mu.Lock()
		_, exists := runtimeMirror.peers["viewer-canceled"]
		runtimeMirror.mu.Unlock()
		if !exists {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("negotiation peer remained after its control context ended")
}

func TestRuntimeMirrorReclaimsPeerWhenDataChannelNeverAttaches(t *testing.T) {
	// Given
	runtimeMirror := newRuntimeMirror(
		testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))},
		time.Hour,
		50*time.Millisecond,
	)
	t.Cleanup(func() { _ = runtimeMirror.Close(context.Background()) })
	browser, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create browser peer: %v", err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	if _, err := browser.CreateDataChannel("mirror", nil); err != nil {
		t.Fatalf("create mirror channel: %v", err)
	}
	offer, err := browser.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(browser)
	if err := browser.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local offer: %v", err)
	}
	<-gathered
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := runtimeMirror.Answer(ctx, "viewer-never-attaches", SessionDescriptionFromPion(*browser.LocalDescription()), ICEConfig{}); err != nil {
		t.Fatalf("answer offer: %v", err)
	}

	// When / Then
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtimeMirror.mu.Lock()
		_, exists := runtimeMirror.peers["viewer-never-attaches"]
		runtimeMirror.mu.Unlock()
		if !exists {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("negotiated peer remained after the attach timeout")
}

func TestRuntimeMirrorCloseUnattachedPeersKeepsAttachedPeer(t *testing.T) {
	// Given
	runtimeMirror := NewRuntimeMirror(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	t.Cleanup(func() { _ = runtimeMirror.Close(context.Background()) })
	unattachedConn, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create unattached peer: %v", err)
	}
	attachedConn, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create attached peer: %v", err)
	}
	committedConn, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create committed peer: %v", err)
	}
	unattachedPeer := &mirrorPeer{pc: unattachedConn, done: make(chan struct{})}
	committedPeer := &mirrorPeer{pc: committedConn, done: make(chan struct{}), negotiationCommitted: true}
	attachedPeer := &mirrorPeer{pc: attachedConn, done: make(chan struct{}), detach: func() {}}
	runtimeMirror.peers["viewer-unattached"] = unattachedPeer
	runtimeMirror.peers["viewer-committed"] = committedPeer
	runtimeMirror.peers["viewer-attached"] = attachedPeer

	// When
	if err := runtimeMirror.CloseUnattachedPeers(context.Background()); err != nil {
		t.Fatalf("close unattached peers: %v", err)
	}

	// Then
	if _, ok := runtimeMirror.peers["viewer-unattached"]; ok {
		t.Fatal("unattached peer remained after control reconnect")
	}
	if _, ok := runtimeMirror.peers["viewer-committed"]; ok {
		t.Fatal("committed-but-unattached peer remained after control reconnect")
	}
	if got := runtimeMirror.peers["viewer-attached"]; got != attachedPeer {
		t.Fatalf("attached peer = %p, want %p", got, attachedPeer)
	}
	select {
	case <-unattachedPeer.done:
	case <-time.After(time.Second):
		t.Fatal("unattached peer was not closed")
	}
	select {
	case <-attachedPeer.done:
		t.Fatal("attached peer was closed during reconnect cleanup")
	default:
	}
}

func TestNegotiationAbandonClosesOnlyItsPeer(t *testing.T) {
	// Given
	runtimeMirror := NewRuntimeMirror(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	t.Cleanup(func() { _ = runtimeMirror.Close(context.Background()) })
	oldPeerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create old peer: %v", err)
	}
	currentPeerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create current peer: %v", err)
	}
	oldPeer := &mirrorPeer{pc: oldPeerConnection, done: make(chan struct{})}
	currentPeer := &mirrorPeer{pc: currentPeerConnection, done: make(chan struct{})}
	runtimeMirror.peers["viewer-1"] = oldPeer
	runtimeMirror.peers["viewer-1"] = currentPeer
	negotiation := Negotiation{
		SessionDescription: SessionDescription{Type: "answer", SDP: "stale"},
		runtimeMirror:      runtimeMirror,
		viewerID:           "viewer-1",
		peer:               oldPeer,
	}

	// When
	if err := negotiation.Abandon(); err != nil {
		t.Fatalf("abandon stale negotiation: %v", err)
	}

	// Then
	if got := runtimeMirror.peers["viewer-1"]; got != currentPeer {
		t.Fatalf("Abandon removed current peer %p, want %p", got, currentPeer)
	}
	select {
	case <-oldPeer.done:
	case <-time.After(time.Second):
		t.Fatal("stale peer was not closed")
	}
	select {
	case <-currentPeer.done:
		t.Fatal("current peer was closed by stale negotiation")
	default:
	}
}

func TestRuntimeMirrorRejectsCanonicalDuplicateViewerID(t *testing.T) {
	mirror := NewRuntimeMirror(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	defer mirror.Close(context.Background())

	browser, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create browser peer: %v", err)
	}
	defer browser.Close()
	if _, err := browser.CreateDataChannel("mirror", nil); err != nil {
		t.Fatalf("create mirror channel: %v", err)
	}
	offer, err := browser.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(browser)
	if err := browser.SetLocalDescription(offer); err != nil {
		t.Fatalf("set browser offer: %v", err)
	}
	<-gathered
	description := SessionDescriptionFromPion(*browser.LocalDescription())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := mirror.Answer(ctx, " viewer-1 ", description, ICEConfig{}); err != nil {
		t.Fatalf("answer first offer: %v", err)
	}
	if _, err := mirror.Answer(ctx, "viewer-1", description, ICEConfig{}); err != ErrDuplicateViewer {
		t.Fatalf("second answer error = %v, want %v", err, ErrDuplicateViewer)
	}
}

func TestRuntimeMirrorAnswerStopsWhenOfferContextIsExpired(t *testing.T) {
	// Given
	mirror := NewRuntimeMirror(testCapturer{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, time.Hour)
	defer mirror.Close(context.Background())
	browser, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create browser peer: %v", err)
	}
	defer browser.Close()
	if _, err := browser.CreateDataChannel("mirror", nil); err != nil {
		t.Fatalf("create mirror channel: %v", err)
	}
	offer, err := browser.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if err := browser.SetLocalDescription(offer); err != nil {
		t.Fatalf("set browser offer: %v", err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	// When
	_, err = mirror.Answer(ctx, "viewer-expired", SessionDescriptionFromPion(*browser.LocalDescription()), ICEConfig{})

	// Then
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Answer error = %v, want context deadline exceeded", err)
	}
}
