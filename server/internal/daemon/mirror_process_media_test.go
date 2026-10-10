//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

type processVideoPeer struct {
	pc             *webrtc.PeerConnection
	control, input *webrtc.DataChannel
	messages       chan []byte
	acks           chan protocol.MirrorInputAck
	packets        chan int
	frames         chan protocol.Message
	source         native.SourceDescriptor
	grant          protocol.MirrorViewerGrant
	offer          protocol.MirrorOfferPayload
}

func newProcessVideoPeer(t *testing.T, ctx context.Context, d *Daemon, client *mirrorProcessClient) *processVideoPeer {
	t.Helper()
	sources, err := d.vscreenSources(ctx, "ws", "rt")
	if err != nil || len(sources) == 0 {
		t.Fatalf("sources unavailable %v", err)
	}
	peer := &processVideoPeer{source: sources[0], messages: make(chan []byte, 32), acks: make(chan protocol.MirrorInputAck, 32), packets: make(chan int, 8), frames: make(chan protocol.Message, 32)}
	peer.pc, err = webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.pc.Close() })
	if _, err = peer.pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	peer.control, err = peer.pc.CreateDataChannel("mirror-control", nil)
	if err != nil {
		t.Fatal(err)
	}
	peer.input, err = peer.pc.CreateDataChannel("mirror-input", nil)
	if err != nil {
		t.Fatal(err)
	}
	peer.control.OnMessage(func(message webrtc.DataChannelMessage) {
		select {
		case peer.messages <- message.Data:
		case <-ctx.Done():
		}
	})
	peer.input.OnMessage(func(message webrtc.DataChannelMessage) {
		var ack protocol.MirrorInputAck
		if json.Unmarshal(message.Data, &ack) == nil {
			select {
			case peer.acks <- ack:
			case <-ctx.Done():
			}
		}
	})
	peer.pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		packet, _, err := track.ReadRTP()
		if err == nil {
			select {
			case peer.packets <- len(packet.Payload):
			case <-ctx.Done():
			}
		}
	})
	offer, err := peer.pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gather := webrtc.GatheringCompletePromise(peer.pc)
	if err = peer.pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gather:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	source := peer.source
	peer.grant = protocol.MirrorViewerGrant{GrantID: "viewer-grant", SessionID: "session", WorkspaceID: "ws", RuntimeID: "rt", UserID: "viewer-user", ViewerID: "viewer", NativeEpoch: source.NativeEpoch, Source: source.Source, SourceGeneration: source.Generation, ExpiresAt: time.Now().Add(40 * time.Second)}
	peer.offer = protocol.MirrorOfferPayload{DaemonGeneration: "server", ProtocolVersion: 2, Transport: "video", Source: &source.Source, SourceGeneration: source.Generation, NativeEpoch: source.NativeEpoch, ViewerGrant: &peer.grant, SessionID: peer.grant.SessionID, WorkspaceID: "ws", RuntimeID: "rt", UserID: peer.grant.UserID, ViewerID: peer.grant.ViewerID, DaemonID: "daemon", ExpiresAt: time.Now().Add(20 * time.Second), Offer: protocol.MirrorSessionDescription{Type: "offer", SDP: peer.pc.LocalDescription().SDP}}
	client.mu.Lock()
	client.enqueue = func(raw []byte) (*wsOutbound, error) {
		var frame protocol.Message
		if err := json.Unmarshal(raw, &frame); err != nil {
			return nil, err
		}
		select {
		case peer.frames <- frame:
			return &wsOutbound{data: raw, sent: true}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	client.mu.Unlock()
	return peer
}
func connectProcessVideo(t *testing.T, ctx context.Context, client *mirrorProcessClient, peer *processVideoPeer) {
	t.Helper()
	if err := client.call(ctx, mirrorProcessRequest{Operation: "control", Payload: marshalRaw(protocol.Message{Type: protocol.EventMirrorOffer, Payload: marshalRaw(peer.offer)})}, nil); err != nil {
		t.Fatal(err)
	}
	var answer protocol.MirrorAnswerPayload
	for answer.Answer.SDP == "" {
		select {
		case frame := <-peer.frames:
			if frame.Type == protocol.EventMirrorAnswerFailure {
				t.Fatal("native fixture negotiation rejected")
			}
			if frame.Type == protocol.EventMirrorAnswer {
				if err := json.Unmarshal(frame.Payload, &answer); err != nil {
					t.Fatal(err)
				}
			}
		case <-ctx.Done():
			t.Fatal("mirror process answer timeout")
		}
	}
	if err := peer.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.Answer.SDP}); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-peer.packets:
		if n <= 0 {
			t.Fatal("empty native RTP")
		}
		t.Logf("child-native RTP payload bytes=%d", n)
	case <-ctx.Done():
		t.Fatal("child did not deliver native RTP")
	}
	for {
		select {
		case raw := <-peer.messages:
			var message struct {
				Type string `json:"type"`
			}
			json.Unmarshal(raw, &message)
			if message.Type == "mirror:video-meta" {
				return
			}
		case <-ctx.Done():
			t.Fatal("control channel did not become ready")
		}
	}
}
func bindProcessControl(t *testing.T, ctx context.Context, client *mirrorProcessClient, peer *processVideoPeer) {
	t.Helper()
	v := peer.grant
	grant := protocol.MirrorControlGrant{GrantID: "control-grant", SessionID: v.SessionID, WorkspaceID: v.WorkspaceID, RuntimeID: v.RuntimeID, UserID: v.UserID, ViewerID: v.ViewerID, NativeEpoch: v.NativeEpoch, Source: v.Source, SourceGeneration: v.SourceGeneration, ExpiresAt: v.ExpiresAt}
	if err := client.call(ctx, mirrorProcessRequest{Operation: "control", Payload: marshalRaw(protocol.Message{Type: protocol.EventMirrorControlGrant, Payload: marshalRaw(protocol.MirrorControlGrantPayload{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "server", Grant: grant})})}, nil); err != nil {
		t.Fatal(err)
	}
}
func TestMirrorProcessMediaArbiterAndCLIApproval(t *testing.T) {
	d, client, _ := mirrorProcessFixtureEnv(t, map[string]string{"MIRROR_FIXTURE_INPUT": "enabled"})
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	peer := newProcessVideoPeer(t, ctx, d, client)
	connectProcessVideo(t, ctx, client, peer)
	bindProcessControl(t, ctx, client, peer)
	d.SetHumanInteractionEnabled(true)
	if !d.HumanInteractionEnabled() {
		t.Fatal("human interaction setting did not reach child")
	}
	task := processScreenTask()
	task.MirrorSource = &peer.source.MirrorSourceBinding
	_, broker, execution, err := d.startTaskVscreen(ctx, task, "codex", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	defer execution.Close()
	content, err := execution.invoke(ctx, "vscreen_acquire", json.RawMessage(`{"request_id":"physical"}`))
	if err != nil {
		t.Fatal(err)
	}
	var acquired struct {
		TransactionID string `json:"transaction_id"`
	}
	if json.Unmarshal([]byte(content[0]["text"].(string)), &acquired) != nil {
		t.Fatal("invalid native transaction")
	}
	input := protocol.MirrorInputMessage{Kind: protocol.MirrorInputPointerDown, GrantID: "control-grant", GestureID: "human-hold", Seq: 1, NativeEpoch: peer.source.NativeEpoch, DisplayGeneration: peer.source.Generation, GeometryRevision: peer.source.GeometryRevision, Pointer: &protocol.MirrorPointerInput{Button: protocol.MirrorButtonLeft, X: 10, Y: 10}}
	if err = peer.input.Send(marshalRaw(input)); err != nil {
		t.Fatal(err)
	}
	select {
	case ack := <-peer.acks:
		if ack.Type != protocol.MirrorInputAckTypeOK {
			t.Fatalf("human input rejected: %+v", ack)
		}
	case <-ctx.Done():
		t.Fatal("human input ACK missing")
	}
	click := vscreenToolArgs{TransactionID: acquired.TransactionID, ActionID: "agent-click", Sequence: 1, Action: &protocol.VscreenAction{Kind: protocol.VscreenActionClick, Click: &protocol.VscreenClickAction{Position: &protocol.VscreenPoint{X: 10, Y: 10}}}}
	if _, err = execution.invoke(ctx, "vscreen_click", marshalRaw(click)); err == nil {
		t.Fatal("human and agent used separate arbiters")
	}
	input.Kind = protocol.MirrorInputPointerUp
	input.Seq = 2
	if err = peer.input.Send(marshalRaw(input)); err != nil {
		t.Fatal(err)
	}
	select {
	case ack := <-peer.acks:
		if ack.Type != protocol.MirrorInputAckTypeOK {
			t.Fatal("human release rejected")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err = execution.invoke(ctx, "vscreen_click", marshalRaw(click)); err != nil {
		t.Fatalf("shared arbiter did not release: %v", err)
	}
	input.Seq = 3
	input.GeometryRevision++
	if err = peer.input.Send(marshalRaw(input)); err != nil {
		t.Fatal(err)
	}
	select {
	case ack := <-peer.acks:
		if ack.Reason != protocol.MirrorInputStale {
			t.Fatal("stale geometry accepted")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	task.InitiatorType = "member"
	task.InitiatorID = peer.grant.UserID
	approved := make(chan error, 1)
	go func() {
		ok, err := d.requestTaskApproval(task)(ctx, agent.ApprovalRequest{Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"command":"fixture-only-no-execution"}`)})
		if err == nil && !ok {
			err = errors.New("approval declined")
		}
		approved <- err
	}()
	var prompt protocol.MirrorAuthorizationRequest
	for prompt.Type != protocol.MirrorAuthorizationRequestType {
		select {
		case raw := <-peer.messages:
			json.Unmarshal(raw, &prompt)
		case err := <-approved:
			t.Fatalf("approval finished before human response: %v", err)
		case <-ctx.Done():
			t.Fatal("CLI approval prompt missing")
		}
	}
	if prompt.Kind != "cli" {
		t.Fatal("wrong approval kind")
	}
	if err = peer.control.SendText(string(marshalRaw(protocol.MirrorAuthorizationResponse{Type: protocol.MirrorAuthorizationResponseType, RequestID: prompt.RequestID, Approved: true}))); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-approved:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("approval result missing")
	}
	revoke := protocol.MirrorControlRevokePayload{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "server", ViewerID: peer.grant.ViewerID, GrantID: "control-grant"}
	if err = client.call(ctx, mirrorProcessRequest{Operation: "control", Payload: marshalRaw(protocol.Message{Type: protocol.EventMirrorControlRevoke, Payload: marshalRaw(revoke)})}, nil); err != nil {
		t.Fatal(err)
	}
	input.Seq = 4
	input.GeometryRevision = peer.source.GeometryRevision
	if err = peer.input.Send(marshalRaw(input)); err != nil {
		t.Fatal(err)
	}
	select {
	case ack := <-peer.acks:
		if ack.Reason != protocol.MirrorInputDenied {
			t.Fatal("revoked input capability survived")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
func TestMirrorProcessOfferEnqueueFailureRollsBackPeer(t *testing.T) {
	d, client, _ := mirrorProcessFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	peer := newProcessVideoPeer(t, ctx, d, client)
	attempted := make(chan struct{}, 1)
	client.mu.Lock()
	client.enqueue = func(raw []byte) (*wsOutbound, error) {
		var frame protocol.Message
		json.Unmarshal(raw, &frame)
		if frame.Type == protocol.EventMirrorAnswer {
			select {
			case attempted <- struct{}{}:
			default:
			}
			return nil, errors.New("fixture writer rejected answer")
		}
		return &wsOutbound{sent: true}, nil
	}
	client.mu.Unlock()
	if err := client.call(ctx, mirrorProcessRequest{Operation: "control", Payload: marshalRaw(protocol.Message{Type: protocol.EventMirrorOffer, Payload: marshalRaw(peer.offer)})}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-attempted:
	case <-ctx.Done():
		t.Fatal("answer never reached actual enqueue boundary")
	}
	waitMirrorProcess(t, func() bool {
		raw, err := client.process.Client.Read(ctx, "mirror.inventory", nil)
		var inventory struct {
			Mirrors map[string]bool `json:"mirrors"`
		}
		return err == nil && json.Unmarshal(raw, &inventory) == nil && !inventory.Mirrors["rt"]
	})
}
func TestMirrorProcessRevokeDuringNegotiationCannotResurrectViewer(t *testing.T) {
	directory := t.TempDir()
	barrier := filepath.Join(directory, "sources")
	arm := filepath.Join(directory, "arm")
	d, client, _ := mirrorProcessFixtureEnv(t, map[string]string{"VSCREEN_FIXTURE_SOURCES_BARRIER": barrier, "MIRROR_FIXTURE_ARM_BARRIER": arm})
	ctx, cancel := context.WithTimeout(t.Context(), 18*time.Second)
	defer cancel()
	peer := newProcessVideoPeer(t, ctx, d, client)
	if err := os.WriteFile(arm, []byte("armed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := client.call(ctx, mirrorProcessRequest{Operation: "control", Payload: marshalRaw(protocol.Message{Type: protocol.EventMirrorOffer, Payload: marshalRaw(peer.offer)})}, nil); err != nil {
		t.Fatal(err)
	}
	waitMirrorProcess(t, func() bool { _, err := os.Stat(barrier); return err == nil })
	revoke := protocol.MirrorViewerRevokePayload{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "server", SessionID: peer.grant.SessionID, ViewerID: peer.grant.ViewerID, GrantID: peer.grant.GrantID}
	if err := client.call(ctx, mirrorProcessRequest{Operation: "control", Payload: marshalRaw(protocol.Message{Type: protocol.EventMirrorViewerRevoke, Payload: marshalRaw(revoke)})}, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(barrier+".release", []byte("released"), 0600); err != nil {
		t.Fatal(err)
	}
	waitMirrorProcess(t, func() bool {
		raw, err := client.process.Client.Read(ctx, "mirror.inventory", nil)
		var inventory struct {
			Mirrors map[string]bool `json:"mirrors"`
		}
		return err == nil && json.Unmarshal(raw, &inventory) == nil && !inventory.Mirrors["rt"]
	})
	select {
	case frame := <-peer.frames:
		if frame.Type == protocol.EventMirrorAnswer {
			t.Fatal("revoked negotiation produced an answer")
		}
	default:
	}
}
