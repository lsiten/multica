package protocol

import (
	"testing"
	"time"
)

func validControlGrant(now time.Time) MirrorControlGrant {
	return MirrorControlGrant{
		GrantID: "grant-1", SessionID: "session-1", WorkspaceID: "ws",
		RuntimeID: "runtime", UserID: "user-1", ViewerID: "viewer-1",
		NativeEpoch:      "native-1",
		Source:           MirrorSource{Kind: MirrorSourcePhysical, SourceID: "display-1"},
		SourceGeneration: "gen-1",
		ExpiresAt:        now.Add(time.Minute),
	}
}

func TestMirrorControlGrantValidate(t *testing.T) {
	now := time.Now()
	g := validControlGrant(now)
	if err := g.Validate(now); err != nil {
		t.Fatalf("valid grant rejected: %v", err)
	}
	expired := g
	expired.ExpiresAt = now.Add(-time.Second)
	if err := expired.Validate(now); err == nil {
		t.Fatal("expired grant accepted")
	}
	for _, mutate := range []func(*MirrorControlGrant){
		func(x *MirrorControlGrant) { x.GrantID = "" },
		func(x *MirrorControlGrant) { x.ViewerID = " " },
		func(x *MirrorControlGrant) { x.SourceGeneration = "" },
		func(x *MirrorControlGrant) { x.Source.Kind = "bogus" },
	} {
		bad := validControlGrant(now)
		mutate(&bad)
		if err := bad.Validate(now); err == nil {
			t.Fatal("invalid grant accepted")
		}
	}
}

func TestMirrorControlGrantEqualIdentity(t *testing.T) {
	now := time.Now()
	a := validControlGrant(now)
	b := validControlGrant(now.Add(time.Minute))
	if !a.EqualIdentity(b) {
		t.Fatal("same identity with later expiry should be equal")
	}
	c := validControlGrant(now)
	c.ViewerID = "viewer-2"
	if a.EqualIdentity(c) {
		t.Fatal("different viewer must differ")
	}
}
