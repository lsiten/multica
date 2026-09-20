package mirror

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

func TestCLIApprovalRoundTripOverPeerChannel(t *testing.T) {
	for _, approved := range []bool{false, true} {
		name := "decline"
		if approved {
			name = "accept"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			m := NewRuntimeMirror(nil, time.Hour)
			m.SetCaptureHub(NewCaptureHub(&fixtureProvider{}))
			defer m.Close(context.Background())
			pc, err := newVideoPeerConnection(ICEConfig{})
			if err != nil {
				t.Fatal(err)
			}
			defer pc.Close()
			if _, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
				t.Fatal(err)
			}
			dc, err := pc.CreateDataChannel("mirror-control", nil)
			if err != nil {
				t.Fatal(err)
			}
			opened := make(chan struct{})
			messages := make(chan webrtc.DataChannelMessage, 8)
			dc.OnOpen(func() { close(opened) })
			dc.OnMessage(func(message webrtc.DataChannelMessage) {
				select {
				case messages <- message:
				case <-ctx.Done():
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
			select {
			case <-gather:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			source := fixtureSource()
			viewer := fixtureGrant(source)
			answer, err := m.AnswerVideo(ctx, viewer.ViewerID, SessionDescriptionFromPion(*pc.LocalDescription()), ICEConfig{}, source, viewer, 1)
			if err != nil {
				t.Fatal(err)
			}
			answer.Commit()
			if err = pc.SetRemoteDescription(answer.SessionDescription.Pion()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-opened:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			control := protocol.MirrorControlGrant{GrantID: "input", SessionID: viewer.SessionID, WorkspaceID: viewer.WorkspaceID, RuntimeID: viewer.RuntimeID, UserID: viewer.UserID, ViewerID: viewer.ViewerID, NativeEpoch: viewer.NativeEpoch, Source: viewer.Source, SourceGeneration: viewer.SourceGeneration, ExpiresAt: viewer.ExpiresAt}
			if !m.BindControlGrant(viewer.ViewerID, control, 1) {
				t.Fatal("control grant rejected")
			}
			type result struct {
				approved bool
				err      error
			}
			completed := make(chan result, 1)
			go func() {
				decision, err := m.RequestCLIApproval(ctx, viewer.WorkspaceID, viewer.RuntimeID, "Codex command", "mkdir approval-marker")
				completed <- result{decision, err}
			}()
			var request protocol.MirrorAuthorizationRequest
			for request.Type != protocol.MirrorAuthorizationRequestType {
				select {
				case message := <-messages:
					if err := json.Unmarshal(message.Data, &request); err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("approval prompt not delivered")
				}
			}
			if request.Kind != "cli" || request.Message != "mkdir approval-marker" || request.Validate(time.Now()) != nil {
				t.Fatal("approval details changed")
			}
			select {
			case <-completed:
				t.Fatal("approval completed before response")
			default:
			}
			response, err := json.Marshal(protocol.MirrorAuthorizationResponse{Type: protocol.MirrorAuthorizationResponseType, RequestID: request.RequestID, Approved: approved})
			if err != nil {
				t.Fatal(err)
			}
			if err = dc.SendText(string(response)); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-completed:
				if result.err != nil || result.approved != approved {
					t.Fatalf("approval result=%v error=%v", result.approved, result.err)
				}
			case <-ctx.Done():
				t.Fatal("approval response not processed")
			}
		})
	}
}
