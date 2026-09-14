package mirror

import (
	"github.com/multica-ai/multica/server/pkg/protocol"
	"testing"
	"time"
)

func TestViewerGrantCannotRenewForeignOrExpiredSession(t *testing.T) {
	// Given
	now := time.Now()
	store := NewViewerGrantStore()
	grant := protocol.MirrorViewerGrant{GrantID: "g", SessionID: "s", UserID: "u", WorkspaceID: "w", RuntimeID: "r", ViewerID: "v", NativeEpoch: "n", Source: protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "p"}, SourceGeneration: "d", ExpiresAt: now.Add(30 * time.Second)}
	if err := store.Add(ViewerGrantRecord{Grant: grant, DaemonID: "daemon", DaemonGeneration: "connection"}, now); err != nil {
		t.Fatal(err)
	}
	// When / Then
	if _, err := store.Lookup("s", "foreign", "r", now); err == nil {
		t.Fatal("foreign user read grant")
	}
	if _, err := store.Lookup("s", "u", "r", now.Add(31*time.Second)); err == nil {
		t.Fatal("expired grant survived")
	}
}

func TestViewerGrantRemoveRacingRenewNeverResurrects(t *testing.T) {
	// Given
	now := time.Now()
	store := NewViewerGrantStore()
	record := ViewerGrantRecord{Grant: protocol.MirrorViewerGrant{SessionID: "session", ExpiresAt: now.Add(time.Second)}}
	if err := store.Add(record, now); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	// When: removal and renewal race.
	go func() { defer close(done); store.Remove("session") }()
	_, _ = store.Renew(record, now.Add(30*time.Second), now)
	<-done
	// Then: removal wins regardless of lock acquisition order.
	if _, err := store.Lookup("session", "", "", now); err == nil {
		t.Fatal("removed lease resurrected")
	}
}

func TestViewerGrantOverlappingRenewalKeepsWinner(t *testing.T) {
	// Given: both callers captured the same live lease.
	now := time.Now()
	store := NewViewerGrantStore()
	record := ViewerGrantRecord{Grant: protocol.MirrorViewerGrant{GrantID: "grant", SessionID: "session", ExpiresAt: now.Add(time.Second)}}
	if err := store.Add(record, now); err != nil {
		t.Fatal(err)
	}
	winner, err := store.Renew(record, now.Add(30*time.Second), now)
	if err != nil {
		t.Fatal(err)
	}
	// When: the second valid renewal completes and a stale sweep tries to remove it.
	second, err := store.Renew(record, now.Add(31*time.Second), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, removed := store.RemoveIfCurrent(record); removed {
		t.Fatal("stale snapshot removed renewed grant")
	}
	// Then: both callers have the valid winner, and explicit removal still cannot resurrect.
	if second.Grant != winner.Grant {
		t.Fatalf("overlap changed winner: %+v", second.Grant)
	}
	store.Remove(record.Grant.SessionID)
	if _, err := store.Renew(second, now.Add(32*time.Second), now); err == nil {
		t.Fatal("explicit removal resurrected")
	}
}

func TestViewerGrantOverlappingRenewalClipsShorterCredentialExpiry(t *testing.T) {
	// Given: a renewal has won while another fresh credential check finds a shorter expiry.
	now := time.Now()
	store := NewViewerGrantStore()
	record := ViewerGrantRecord{Grant: protocol.MirrorViewerGrant{GrantID: "grant", SessionID: "session", ExpiresAt: now.Add(time.Second)}}
	if err := store.Add(record, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Renew(record, now.Add(30*time.Second), now); err != nil {
		t.Fatal(err)
	}
	// When: the overlapping request carries the reduced credential deadline.
	record.CredentialExpiry = now.Add(5 * time.Second)
	renewed, err := store.Renew(record, record.CredentialExpiry, now)
	// Then: the valid winner is retained but cannot outlive that deadline.
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Grant.ExpiresAt.After(record.CredentialExpiry) {
		t.Fatal("overlapping renewal exceeded fresh credential expiry")
	}
}
