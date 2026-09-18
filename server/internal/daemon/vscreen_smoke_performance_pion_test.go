package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

func TestPerformanceTwoPionViewersShareOneProviderAndDetach(t *testing.T) {
	p, client := performanceTestProducer(t)
	client.streams.emit = false
	key := protocol.ResourceKey{BackendIdentity: p.backendID, WorkspaceID: p.workspaceID, RuntimeID: "runtime", UID: 501}
	source := native.SourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: key, Source: protocol.MirrorSource{Kind: protocol.MirrorSourceVirtual, SourceID: "owned-source"}, NativeEpoch: "native", Generation: "generation"}, DisplayID: 1, GeometryRevision: 1, LogicalWidth: 1600, LogicalHeight: 900}
	p.sources["owned-source"] = performanceSource{SourceID: "owned-source", RuntimeID: "runtime", SourceTag: 1, NativeEpoch: "native", Generation: "generation", source: source}
	runtime := mirror.NewRuntimeMirror(nil, time.Second)
	runtime.SetCaptureHub(p.hub)
	p.runtimes["runtime"] = runtime
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	for _, id := range []string{"one", "two"} {
		pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { pc.Close() })
		if _, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			t.Fatal(err)
		}
		channel, err := pc.CreateDataChannel("mirror-control", nil)
		if err != nil {
			t.Fatal(err)
		}
		opened := make(chan struct{})
		channel.OnOpen(func() { close(opened) })
		offer, err := pc.CreateOffer(nil)
		if err != nil {
			t.Fatal(err)
		}
		gathered := webrtc.GatheringCompletePromise(pc)
		if err = pc.SetLocalDescription(offer); err != nil {
			t.Fatal(err)
		}
		select {
		case <-gathered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		response := performanceTestRequest(p, "POST", "/offer", performanceOfferRequest{ViewerID: id, SourceID: "owned-source", Offer: mirror.SessionDescriptionFromPion(*pc.LocalDescription())})
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		var answer performanceOfferResponse
		if err = json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
			t.Fatal(err)
		}
		if answer.Negotiated.Width == 0 || answer.Negotiated.Height == 0 {
			t.Fatal("missing negotiated resolution")
		}
		if err = pc.SetRemoteDescription(answer.Answer.Pion()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-opened:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		client.streams.mu.Lock()
		opens, active := client.streams.opens, client.streams.active
		client.streams.mu.Unlock()
		if opens == 1 && active == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("expected shared open, opens=%d active=%d", opens, active)
		case <-ticker.C:
		}
	}
	for _, id := range []string{"one", "two"} {
		if response := performanceTestRequest(p, "POST", "/viewer/close", map[string]string{"viewer_id": id}); response.Code != 200 {
			t.Fatal(response.Body.String())
		}
	}
	client.streams.mu.Lock()
	defer client.streams.mu.Unlock()
	if client.streams.closes != 1 || client.streams.active != 0 {
		t.Fatalf("capture retained closes=%d active=%d", client.streams.closes, client.streams.active)
	}
}
func TestPerformanceBrowserJournalCannotInventNativeCounters(t *testing.T) {
	p, _ := performanceTestProducer(t)
	p.peers["one"] = performancePeer{sourceID: "owned"}
	sample := performanceBrowserSample{SchemaVersion: 1, Phase: "steady", ViewerID: "one", SourceID: "owned", Frames: []performanceBrowserFrame{{FrameID: 1, SourceTag: 1, DrawNS: "1", ObservedMS: 2, LatencyMS: 1}}, Counters: map[string]uint64{"bytes_received": 999999}}
	if response := performanceTestRequest(p, "POST", "/samples", sample); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if p.capture.bytes.Load() != 0 {
		t.Fatal("browser supplied native bandwidth")
	}
	sample.SourceID = "foreign"
	if response := performanceTestRequest(p, "POST", "/samples", sample); response.Code != 400 {
		t.Fatal("foreign sample accepted")
	}
	sample.SourceID = "owned"
	p.journalBytes = 128 * 1024 * 1024
	if response := performanceTestRequest(p, "POST", "/samples", sample); response.Code != 413 {
		t.Fatal("journal exceeded bound")
	}
	delete(p.peers, "one")
}
