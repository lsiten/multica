package mirror

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

func TestAuthorizationDecisionBelongsToLiveRecipient(t *testing.T) {
	for _, scenario := range []string{"recipient", "foreign_peer", "expired_request", "expired_viewer", "closed_peer", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			m := NewRuntimeMirror(nil, time.Hour)
			peer := &mirrorPeer{grant: &viewerGrant{deadline: time.Now().Add(time.Hour)}}
			other := &mirrorPeer{grant: &viewerGrant{deadline: time.Now().Add(time.Hour)}}
			expires := time.Now().Add(time.Minute)
			if scenario == "expired_request" {
				expires = time.Now().Add(-time.Second)
			}
			m.pendingAuthorizations["request"] = pendingAuthorization{peer: peer, expiresAt: expires}
			calls := 0
			m.SetAuthorizationHandler(func(_ context.Context, id string, approved bool) error {
				if id != "request" || !approved {
					t.Fatal("decision changed before reaching handler")
				}
				calls++
				return nil
			})
			response, err := json.Marshal(protocol.MirrorAuthorizationResponse{
				Type: protocol.MirrorAuthorizationResponseType, RequestID: "request", Approved: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			message := webrtc.DataChannelMessage{IsString: true, Data: response}
			sender := peer
			switch scenario {
			case "foreign_peer":
				sender = other
			case "expired_viewer":
				peer.grant.deadline = time.Now().Add(-time.Second)
			case "closed_peer":
				peer.closed = true
			}
			m.handleAuthorizationMessage(sender, message)
			if scenario == "duplicate" {
				m.handleAuthorizationMessage(sender, message)
			}
			want := 0
			if scenario == "recipient" || scenario == "duplicate" {
				want = 1
			}
			if calls != want {
				t.Fatalf("handler called %d times, want %d", calls, want)
			}
			if scenario == "foreign_peer" {
				m.handleAuthorizationMessage(peer, message)
				if calls != 1 {
					t.Fatal("foreign decision consumed the recipient's request")
				}
			}
		})
	}
}

func TestAuthorizationConcurrentDecisionsInvokeHandlerOnce(t *testing.T) {
	m := NewRuntimeMirror(nil, time.Hour)
	peer := &mirrorPeer{grant: &viewerGrant{deadline: time.Now().Add(time.Hour)}}
	m.pendingAuthorizations["request"] = pendingAuthorization{peer: peer, expiresAt: time.Now().Add(time.Minute)}
	var calls atomic.Int32
	m.SetAuthorizationHandler(func(context.Context, string, bool) error {
		calls.Add(1)
		return nil
	})
	message := webrtc.DataChannelMessage{IsString: true, Data: []byte(`{"type":"mirror-authorization:response","request_id":"request","approved":true}`)}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() { m.handleAuthorizationMessage(peer, message) })
	}
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("handler called %d times, want once", calls.Load())
	}
}

func TestClosingPeerRemovesOnlyItsAuthorizationRequests(t *testing.T) {
	m := NewRuntimeMirror(nil, time.Hour)
	peer := newControlGrantTestPeer(t)
	m.peers["viewer"] = peer
	m.pendingAuthorizations["owned"] = pendingAuthorization{peer: peer, expiresAt: time.Now().Add(time.Minute)}
	m.pendingAuthorizations["other"] = pendingAuthorization{peer: &mirrorPeer{}, expiresAt: time.Now().Add(time.Minute)}
	if err := m.removePeer("viewer", peer, false); err != nil {
		t.Fatal(err)
	}
	if len(m.pendingAuthorizations) != 1 || m.pendingAuthorizations["other"].peer == nil {
		t.Fatal("closing peer did not isolate authorization cleanup")
	}
}
