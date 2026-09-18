package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func mirrorV2Fixture() (MirrorOfferPayload, MirrorAuthorization) {
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	resource := ResourceKey{BackendIdentity: "https://example.com", WorkspaceID: "ws", RuntimeID: "runtime", UID: 501}
	source := MirrorSource{Kind: MirrorSourceVirtual, SourceID: "display-virtual"}
	grant := MirrorViewerGrant{GrantID: "grant-1", SessionID: "session-1", WorkspaceID: "ws", RuntimeID: "runtime", UserID: "user-1", ViewerID: "viewer-1", NativeEpoch: "native-1", Source: source, SourceGeneration: "gen-1", ExpiresAt: now.Add(time.Minute)}
	offer := MirrorOfferPayload{SessionID: "session-1", WorkspaceID: "ws", RuntimeID: "runtime", UserID: "user-1", DaemonID: "daemon-1", ViewerID: "viewer-1", Offer: MirrorSessionDescription{Type: "offer", SDP: "fixture-sdp"}, ExpiresAt: now.Add(30 * time.Second), ProtocolVersion: 2, Transport: "video", Source: &source, SourceGeneration: "gen-1", NativeEpoch: "native-1", ViewerGrant: &grant}
	auth := MirrorAuthorization{Resource: resource, DaemonID: "daemon-1", UserID: "user-1", Now: now, NativeEpoch: "native-1", Sources: []MirrorSourceBinding{{Resource: resource, Source: source, Generation: "gen-1", NativeEpoch: "native-1"}}}
	return offer, auth
}

func TestMirrorV2ParserRejectsUnsafeBindings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*MirrorOfferPayload, *MirrorAuthorization)
	}{
		{"unknown version", func(p *MirrorOfferPayload, _ *MirrorAuthorization) { p.ProtocolVersion = 3 }},
		{"v1 source downgrade", func(p *MirrorOfferPayload, _ *MirrorAuthorization) { p.ProtocolVersion = 0 }},
		{"missing source", func(p *MirrorOfferPayload, _ *MirrorAuthorization) { p.Source = nil }},
		{"unknown source", func(p *MirrorOfferPayload, _ *MirrorAuthorization) { p.Source.Kind = "unknown" }},
		{"foreign workspace", func(p *MirrorOfferPayload, _ *MirrorAuthorization) {
			p.WorkspaceID = "foreign"
			p.ViewerGrant.WorkspaceID = "foreign"
		}},
		{"foreign runtime", func(p *MirrorOfferPayload, _ *MirrorAuthorization) {
			p.RuntimeID = "foreign"
			p.ViewerGrant.RuntimeID = "foreign"
		}},
		{"foreign backend source", func(_ *MirrorOfferPayload, a *MirrorAuthorization) {
			a.Sources[0].Resource.BackendIdentity = "https://foreign.example"
		}},
		{"foreign login source", func(_ *MirrorOfferPayload, a *MirrorAuthorization) { a.Sources[0].Resource.UID++ }},
		{"foreign runtime source", func(_ *MirrorOfferPayload, a *MirrorAuthorization) { a.Sources[0].Resource.RuntimeID = "foreign" }},
		{"stale source generation", func(p *MirrorOfferPayload, _ *MirrorAuthorization) {
			p.SourceGeneration = "old"
			p.ViewerGrant.SourceGeneration = "old"
		}},
		{"stale native epoch", func(p *MirrorOfferPayload, _ *MirrorAuthorization) {
			p.NativeEpoch = "old"
			p.ViewerGrant.NativeEpoch = "old"
		}},
		{"missing grant", func(p *MirrorOfferPayload, _ *MirrorAuthorization) { p.ViewerGrant = nil }},
		{"foreign grant viewer", func(p *MirrorOfferPayload, _ *MirrorAuthorization) { p.ViewerGrant.ViewerID = "other-viewer" }},
		{"expired grant", func(p *MirrorOfferPayload, a *MirrorAuthorization) { p.ViewerGrant.ExpiresAt = a.Now }},
		{"expired offer", func(p *MirrorOfferPayload, a *MirrorAuthorization) { p.ExpiresAt = a.Now }},
		{"nonvideo transport", func(p *MirrorOfferPayload, _ *MirrorAuthorization) { p.Transport = "jpeg" }},
		{"revoked source", func(_ *MirrorOfferPayload, a *MirrorAuthorization) { a.Sources = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, auth := mirrorV2Fixture()
			tc.change(&p, &auth)
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseMirrorOffer(raw, auth); err == nil {
				t.Fatalf("accepted %s", tc.name)
			}
		})
	}
}

func TestMirrorV2TwoViewersKeepIndependentSources(t *testing.T) {
	a, auth := mirrorV2Fixture()
	b, _ := mirrorV2Fixture()
	b.SessionID, b.ViewerID = "session-2", "viewer-2"
	b.Source = &MirrorSource{Kind: MirrorSourcePhysical, SourceID: "display-physical"}
	b.ViewerGrant.SessionID, b.ViewerGrant.ViewerID, b.ViewerGrant.Source = b.SessionID, b.ViewerID, *b.Source
	auth.Sources = append(auth.Sources, MirrorSourceBinding{Resource: auth.Resource, Source: *b.Source, Generation: "gen-1", NativeEpoch: "native-1"})
	for _, offer := range []MirrorOfferPayload{a, b} {
		raw, err := json.Marshal(offer)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseMirrorOffer(raw, auth)
		if err != nil {
			t.Fatal(err)
		}
		if *parsed.Source != *offer.Source || parsed.ViewerID != offer.ViewerID {
			t.Fatalf("viewer source changed: %+v", parsed)
		}
	}
}

func TestMirrorParserLegacyMissingSourceAndMalformedJSON(t *testing.T) {
	p, auth := mirrorV2Fixture()
	p.ProtocolVersion, p.Transport, p.Source, p.SourceGeneration, p.NativeEpoch, p.ViewerGrant = 0, "", nil, "", "", nil
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseMirrorOffer(raw, auth); err != nil {
		t.Fatalf("legacy missing-source offer rejected: %v", err)
	}
	for _, malformed := range [][]byte{[]byte(`null`), []byte(`{`), append(raw, []byte(` {}`)...), []byte(`{"protocol_version":"2"}`)} {
		if _, err := ParseMirrorOffer(malformed, auth); err == nil {
			t.Fatalf("accepted malformed JSON: %s", malformed)
		}
	}
}

func TestMirrorLegacyViewerGrantStaysPrimaryAndAuthorized(t *testing.T) {
	p, auth := mirrorV2Fixture()
	p.ProtocolVersion, p.Transport, p.Source, p.SourceGeneration, p.NativeEpoch = 0, "", nil, "", ""
	p.ViewerGrant.Source.Kind = MirrorSourcePhysical
	auth.Sources[0].Source = p.ViewerGrant.Source
	auth.Sources[0].Primary = true
	auth.RequireViewerGrant = true
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseMirrorOffer(raw, auth); err != nil {
		t.Fatalf("granted legacy viewer rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*MirrorOfferPayload, *MirrorAuthorization)
	}{
		{"missing required grant", func(p *MirrorOfferPayload, _ *MirrorAuthorization) { p.ViewerGrant = nil }},
		{"expired legacy grant", func(p *MirrorOfferPayload, a *MirrorAuthorization) { p.ViewerGrant.ExpiresAt = a.Now }},
		{"virtual legacy source", func(p *MirrorOfferPayload, _ *MirrorAuthorization) { p.ViewerGrant.Source.Kind = MirrorSourceVirtual }},
		{"foreign legacy viewer", func(p *MirrorOfferPayload, _ *MirrorAuthorization) { p.ViewerGrant.ViewerID = "other" }},
		{"stale legacy native", func(p *MirrorOfferPayload, _ *MirrorAuthorization) { p.ViewerGrant.NativeEpoch = "old" }},
		{"revoked legacy source", func(_ *MirrorOfferPayload, a *MirrorAuthorization) { a.Sources = nil }},
		{"nonprimary legacy source", func(_ *MirrorOfferPayload, a *MirrorAuthorization) {
			a.Sources = append([]MirrorSourceBinding(nil), a.Sources...)
			a.Sources[0].Primary = false
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var copy MirrorOfferPayload
			if err := json.Unmarshal(raw, &copy); err != nil {
				t.Fatal(err)
			}
			a := auth
			tc.change(&copy, &a)
			encoded, err := json.Marshal(copy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseMirrorOffer(encoded, a); err == nil {
				t.Fatal("accepted invalid legacy grant")
			}
		})
	}
}
