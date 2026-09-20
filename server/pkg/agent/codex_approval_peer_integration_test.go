//go:build agentintegration

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

// This fixture supplies no desktop pixels. The real CLI approval traverses the
// production mirror broker and a negotiated P2P channel before it can proceed.
type approvalCaptureFixture struct{}

func (approvalCaptureFixture) Open(context.Context, mirror.EncodedSource) (mirror.EncodedStream, error) {
	return approvalCaptureFixture{}, nil
}
func (approvalCaptureFixture) Next(ctx context.Context) (mirror.EncodedSample, error) {
	<-ctx.Done()
	return mirror.EncodedSample{}, ctx.Err()
}
func (approvalCaptureFixture) ForceKeyframe() error { return nil }
func (approvalCaptureFixture) Close() error         { return nil }

func realApprovalPeer(t *testing.T, approve bool) func(context.Context, ApprovalRequest) (bool, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	runtime := mirror.NewRuntimeMirror(nil, time.Hour)
	runtime.SetCaptureHub(mirror.NewCaptureHub(approvalCaptureFixture{}))
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	if _, err = peer.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	channel, err := peer.CreateDataChannel("mirror-control", nil)
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{})
	channel.OnOpen(func() { close(opened) })
	requests := make(chan protocol.MirrorAuthorizationRequest, 1)
	channel.OnMessage(func(message webrtc.DataChannelMessage) {
		var request protocol.MirrorAuthorizationRequest
		if json.Unmarshal(message.Data, &request) == nil && request.Type == protocol.MirrorAuthorizationRequestType {
			select {
			case requests <- request:
			case <-ctx.Done():
			}
		}
	})
	offer, err := peer.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(peer)
	if err = peer.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		t.Fatal("approval peer ICE timeout")
	}
	source := mirror.EncodedSource{
		Binding:          protocol.MirrorSourceBinding{Resource: protocol.ResourceKey{BackendIdentity: "https://approval.test", WorkspaceID: "ws", RuntimeID: "runtime", UID: 501}, Source: protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "display"}, NativeEpoch: "epoch", Generation: "generation"},
		GeometryRevision: 1, MaxLevelIDC: 40, DisplayID: 1, Width: 1600, Height: 900, FPS: 30, Bitrate: 4000000,
	}
	viewer := protocol.MirrorViewerGrant{GrantID: "view", SessionID: "session", WorkspaceID: "ws", RuntimeID: "runtime", UserID: "initiator", ViewerID: "viewer", NativeEpoch: "epoch", Source: source.Binding.Source, SourceGeneration: "generation", ExpiresAt: time.Now().Add(2 * time.Minute)}
	answer, err := runtime.AnswerVideo(ctx, viewer.ViewerID, mirror.SessionDescriptionFromPion(*peer.LocalDescription()), mirror.ICEConfig{}, source, viewer, 1)
	if err != nil {
		t.Fatal(err)
	}
	answer.Commit()
	if err = peer.SetRemoteDescription(answer.SessionDescription.Pion()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-opened:
	case <-ctx.Done():
		t.Fatal("approval peer not connected")
	}
	grant := protocol.MirrorControlGrant{GrantID: "control", SessionID: viewer.SessionID, WorkspaceID: viewer.WorkspaceID, RuntimeID: viewer.RuntimeID, UserID: viewer.UserID, ViewerID: viewer.ViewerID, NativeEpoch: viewer.NativeEpoch, Source: viewer.Source, SourceGeneration: viewer.SourceGeneration, ExpiresAt: viewer.ExpiresAt}
	if !runtime.BindControlGrant(viewer.ViewerID, grant, 1) {
		t.Fatal("approval peer capability rejected")
	}
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				viewRenewal, controlRenewal := viewer, grant
				viewRenewal.ExpiresAt = time.Now().Add(2 * time.Minute)
				controlRenewal.ExpiresAt = viewRenewal.ExpiresAt
				if !runtime.RenewViewerGrant(viewer.ViewerID, viewRenewal, 1) || !runtime.RenewControlGrant(viewer.ViewerID, controlRenewal, 1) {
					t.Error("approval peer renewal failed")
					return
				}
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-renewed })
	return func(callCtx context.Context, request ApprovalRequest) (bool, error) {
		type decision struct {
			approved bool
			err      error
		}
		completed := make(chan decision, 1)
		go func() {
			approved, err := runtime.RequestCLIApproval(callCtx, mirror.CLIApprovalAudience{WorkspaceID: viewer.WorkspaceID, RuntimeID: viewer.RuntimeID, UserID: viewer.UserID}, request.Method, string(request.Params))
			completed <- decision{approved, err}
		}()
		select {
		case result := <-completed:
			if result.err != nil {
				return false, result.err
			}
			return false, errors.New("approval completed without a peer prompt")
		case prompt := <-requests:
			if prompt.Validate(time.Now()) != nil || prompt.Title != request.Method || prompt.Message != string(request.Params) {
				t.Error("real command approval changed in peer transport")
				return false, nil
			}
			select {
			case <-completed:
				t.Error("real command approval completed before peer response")
				return false, nil
			default:
			}
			reply, err := json.Marshal(protocol.MirrorAuthorizationResponse{Type: protocol.MirrorAuthorizationResponseType, RequestID: prompt.RequestID, Approved: approve})
			if err != nil {
				return false, err
			}
			if err = channel.SendText(string(reply)); err != nil {
				return false, err
			}
		case <-callCtx.Done():
			return false, callCtx.Err()
		}
		select {
		case result := <-completed:
			return result.approved, result.err
		case <-callCtx.Done():
			return false, callCtx.Err()
		}
	}
}
