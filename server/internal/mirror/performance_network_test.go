package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestSelectedNetworkGetterUsesActualPionTransportAndCloses(t *testing.T) {
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
	dc.OnOpen(func() { close(opened) })
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
	answer, err := m.AnswerVideo(ctx, "viewer", SessionDescriptionFromPion(*pc.LocalDescription()), ICEConfig{}, source, fixtureGrant(source), 1)
	if err != nil {
		t.Fatal(err)
	}
	answer.Commit()
	if early := m.PeerNetworkObservation("viewer"); early.Available || early.SelectedPair != nil {
		t.Fatal("SDP candidates masqueraded as selected transport")
	}
	if err = pc.SetRemoteDescription(answer.SessionDescription.Pion()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-opened:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var before PeerNetworkObservation
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		before = m.PeerNetworkObservation("viewer")
		if before.Available {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("selected observation unavailable: %+v", before)
		}
	}
	if before.SelectedPair == nil || !before.SelectedPair.Nominated || before.SelectedPair.State != "succeeded" || !before.DTLS.Available {
		t.Fatalf("missing real transport proof %+v", before)
	}
	remoteParams, err := pc.SCTP().Transport().GetLocalParameters()
	if err != nil {
		t.Fatal(err)
	}
	if before.DTLS.RemoteFingerprint != strings.ToLower(strings.ReplaceAll(remoteParams.Fingerprints[0].Value, ":", "")) {
		t.Fatal("remote certificate not actual peer certificate")
	}
	hostCertificate := sha256.Sum256(pc.SCTP().Transport().GetRemoteCertificate())
	if before.DTLS.LocalFingerprint != hex.EncodeToString(hostCertificate[:]) {
		t.Fatal("local certificate not peer-observed certificate")
	}
	if err = dc.SendText(strings.Repeat("controlled-local-metadata-test", 128)); err != nil {
		t.Fatal(err)
	}
	for {
		after := m.PeerNetworkObservation("viewer")
		if after.Available && after.SelectedPair.PairID == before.SelectedPair.PairID && after.SelectedPair.BytesReceived > before.SelectedPair.BytesReceived {
			t.Logf("LOCALHOST ONLY: selected/nominated/succeeded; bytes_received %d -> %d; certificate fingerprints cross-checked", before.SelectedPair.BytesReceived, after.SelectedPair.BytesReceived)
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("real pair counter did not increase")
		}
	}
	if got := m.PeerNetworkObservation("foreign"); got.Available || got.SelectedPair != nil {
		t.Fatal("foreign viewer exposed")
	}
	if err = answer.Abandon(); err != nil {
		t.Fatal(err)
	}
	if got := m.PeerNetworkObservation("viewer"); got.Available || got.SelectedPair != nil || got.DTLS.Available {
		t.Fatal("closed peer remained available")
	}
}
func TestSelectedNetworkRejectsMixedSnapshotsAndNonNominatedPairs(t *testing.T) {
	for _, tc := range []string{"valid", "not_nominated", "not_succeeded", "changed_pair", "changed_tuple", "counter_reset", "wrong_candidate", "deleted_candidate", "missing_stats"} {
		t.Run(tc, func(t *testing.T) {
			local := &webrtc.ICECandidate{Address: "192.0.2.2", Port: 4000, Protocol: webrtc.ICEProtocolUDP, Typ: webrtc.ICECandidateTypeHost}
			remote := &webrtc.ICECandidate{Address: "192.0.2.3", Port: 5000, Protocol: webrtc.ICEProtocolUDP, Typ: webrtc.ICECandidateTypeHost}
			pair := webrtc.NewICECandidatePair(local, remote)
			after := webrtc.NewICECandidatePair(local, remote)
			stats := webrtc.ICECandidatePairStats{ID: "pair", LocalCandidateID: "local", RemoteCandidateID: "remote", State: webrtc.StatsICECandidatePairStateSucceeded, Nominated: true, BytesSent: 100, BytesReceived: 100}
			last := stats
			report := webrtc.StatsReport{"local": webrtc.ICECandidateStats{IP: local.Address, Port: int32(local.Port), Protocol: "udp", CandidateType: local.Typ}, "remote": webrtc.ICECandidateStats{IP: remote.Address, Port: int32(remote.Port), Protocol: "udp", CandidateType: remote.Typ}}
			switch tc {
			case "not_nominated":
				stats.Nominated = false
			case "not_succeeded":
				stats.State = webrtc.StatsICECandidatePairStateInProgress
			case "changed_pair":
				last.ID = "other"
			case "changed_tuple":
				copy := *remote
				copy.Port++
				after.Remote = &copy
			case "counter_reset":
				last.BytesSent--
			case "wrong_candidate":
				v := report["remote"].(webrtc.ICECandidateStats)
				v.IP = "192.0.2.9"
				report["remote"] = v
			case "deleted_candidate":
				v := report["local"].(webrtc.ICECandidateStats)
				v.Deleted = true
				report["local"] = v
			case "missing_stats":
				delete(report, "local")
			}
			got, reason := stableSelectedPair(pair, stats, report, after, last)
			if (got != nil) != (tc == "valid") || (reason == "") != (tc == "valid") {
				t.Fatalf("case=%s got=%+v reason=%s", tc, got, reason)
			}
		})
	}
}
