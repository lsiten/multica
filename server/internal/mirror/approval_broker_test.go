package mirror

import (
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

func TestCLIApprovalRequiresOriginalLiveControlGrant(t *testing.T) {
	for _, scenario := range []string{"accept", "revoked", "replaced", "expired", "foreign", "disconnected"} {
		t.Run(scenario, func(t *testing.T) {
			m := NewRuntimeMirror(nil, time.Hour)
			grant := &controlGrant{deadline: time.Now().Add(time.Minute), value: protocol.MirrorControlGrant{ExpiresAt: time.Now().Add(time.Minute)}}
			peer := &mirrorPeer{grant: &viewerGrant{deadline: time.Now().Add(time.Minute)}, controlGrant: grant}
			decision := make(chan bool, 1)
			m.pendingAuthorizations["request"] = pendingAuthorization{peer: peer, control: grant, decision: decision, expiresAt: time.Now().Add(time.Minute)}
			sender := peer
			switch scenario {
			case "revoked":
				peer.controlGrant = nil
			case "replaced":
				replacement := *grant
				peer.controlGrant = &replacement
			case "expired":
				grant.value.ExpiresAt = time.Now().Add(-time.Second)
			case "foreign":
				sender = &mirrorPeer{grant: &viewerGrant{deadline: time.Now().Add(time.Minute)}}
			case "disconnected":
				m.releaseAuthorizations(peer)
			}
			m.handleAuthorizationMessage(sender, webrtc.DataChannelMessage{IsString: true, Data: []byte(`{"type":"mirror-authorization:response","request_id":"request","approved":true}`)})
			select {
			case approved := <-decision:
				if approved != (scenario == "accept") {
					t.Fatal("incorrect approval decision")
				}
			default:
				if scenario == "accept" || scenario == "disconnected" {
					t.Fatal("expected terminal decision")
				}
			}
		})
	}
}

func TestCLIApprovalDoesNotSelectReadOnlyViewer(t *testing.T) {
	m := NewRuntimeMirror(nil, time.Hour)
	m.peers["viewer"] = &mirrorPeer{grant: &viewerGrant{deadline: time.Now().Add(time.Minute)}}
	approved, err := m.RequestCLIApproval(t.Context(), CLIApprovalAudience{WorkspaceID: "ws", RuntimeID: "runtime", UserID: "user"}, "Command", "details")
	if err == nil || approved {
		t.Fatal("read-only viewer selected for command approval")
	}
}

func TestCLIApprovalDoesNotSelectAnotherMember(t *testing.T) {
	m := NewRuntimeMirror(nil, time.Hour)
	grant := testControlGrant("control", "display", time.Now().Add(time.Minute))
	m.peers["viewer"] = &mirrorPeer{controlGrant: &controlGrant{value: grant, deadline: grant.ExpiresAt}}
	approved, err := m.RequestCLIApproval(t.Context(), CLIApprovalAudience{
		WorkspaceID: grant.WorkspaceID, RuntimeID: grant.RuntimeID, UserID: "different-member",
	}, "Command", "private approval details")
	if err == nil || err.Error() != "mirror: no active approval recipient" || approved {
		t.Fatalf("another member was considered an approval recipient: %v", err)
	}
	if len(m.pendingAuthorizations) != 0 {
		t.Fatal("private approval was published to another member")
	}
}
