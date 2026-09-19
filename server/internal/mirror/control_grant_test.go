package mirror

import (
	"context"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

func newControlGrantTestPeer(t *testing.T) *mirrorPeer {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create peer connection: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	return &mirrorPeer{pc: pc, done: make(chan struct{})}
}

func testControlGrant(id string, sourceID string, expiresAt time.Time) protocol.MirrorControlGrant {
	return protocol.MirrorControlGrant{
		GrantID:          id,
		SessionID:        "control-viewer",
		WorkspaceID:      "ws",
		RuntimeID:        "runtime",
		UserID:           "user",
		ViewerID:         "viewer",
		NativeEpoch:      "native",
		Source:           protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: sourceID},
		SourceGeneration: "generation-" + sourceID,
		ExpiresAt:        expiresAt,
	}
}

func TestControlGrantReplacesBoundSourceAtomically(t *testing.T) {
	m := NewRuntimeMirror(nil, time.Hour)
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	m.peers["viewer"] = newControlGrantTestPeer(t)

	var changes []ControlStateChange
	m.SetControlStateHook(func(change ControlStateChange) {
		changes = append(changes, change)
	}, 1)

	firstExpiresAt := time.Now().Add(time.Minute)
	first := testControlGrant("grant-1", "display-1", firstExpiresAt)
	if !m.BindControlGrant("viewer", first, 1) {
		t.Fatal("first grant rejected")
	}
	second := testControlGrant("grant-2", "display-2", firstExpiresAt.Add(time.Second))
	if !m.ReplaceControlGrant("viewer", second, 1) {
		t.Fatal("source replacement rejected")
	}
	current, ok := m.CurrentControlGrant("viewer")
	if !ok {
		t.Fatal("current grant missing")
	}
	if current.GrantID != "grant-2" || current.Source.SourceID != "display-2" {
		t.Fatalf("current grant = %+v, want display-2 grant", current)
	}
	if m.RenewControlGrant("viewer", first, 1) {
		t.Fatal("old source grant renewed after replacement")
	}
	if current, _ := m.CurrentControlGrant("viewer"); current.Source.SourceID != "display-2" {
		t.Fatalf("old grant changed source back to %q", current.Source.SourceID)
	}
	if len(changes) != 2 || !changes[0].Active || !changes[1].Active || changes[1].Source.SourceID != "display-2" {
		t.Fatalf("state changes = %+v, want two active bindings without inactive gap", changes)
	}
}

func TestControlGrantReplacementRejectsSameIdentity(t *testing.T) {
	m := NewRuntimeMirror(nil, time.Hour)
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	m.peers["viewer"] = newControlGrantTestPeer(t)
	first := testControlGrant("grant-1", "display-1", time.Now().Add(time.Minute))
	if !m.BindControlGrant("viewer", first, 1) {
		t.Fatal("first grant rejected")
	}
	sameIdentity := first
	sameIdentity.GrantID = "grant-2"
	if m.ReplaceControlGrant("viewer", sameIdentity, 1) {
		t.Fatal("same identity accepted as replacement")
	}
	current, _ := m.CurrentControlGrant("viewer")
	if current.GrantID != "grant-1" {
		t.Fatalf("grant id = %q, want original", current.GrantID)
	}
}
