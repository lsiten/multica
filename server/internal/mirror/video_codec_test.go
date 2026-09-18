package mirror

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestVideoCodecReceiveLevelConstraints(t *testing.T) {
	for _, tc := range []struct {
		name, params string
		level        uint8
	}{
		{"default", "", 31}, {"asymmetric", ";level-asymmetry-allowed=1;max-recv-level=e028", 40},
		{"symmetric", ";level-asymmetry-allowed=0;max-recv-level=e028", 31},
		{"absent_asymmetry", ";max-recv-level=e028", 31},
		{"unknown_extension", ";x-max-recv-level=e028", 31},
		{"decimal", ";level-asymmetry-allowed=1;max-recv-level=40", 0},
		{"invalid_hex", ";level-asymmetry-allowed=1;max-recv-level=zz28", 0},
		{"unknown_level", ";level-asymmetry-allowed=1;max-recv-level=e0ff", 0},
		{"changed_constraints", ";level-asymmetry-allowed=1;max-recv-level=0028", 0},
		{"equal_level", ";level-asymmetry-allowed=1;max-recv-level=e01f", 0},
		{"lower_level", ";level-asymmetry-allowed=1;max-recv-level=e01e", 0},
		{"invalid_asymmetry", ";level-asymmetry-allowed=2;max-recv-level=e028", 0},
		{"duplicate", ";level-asymmetry-allowed=0;level-asymmetry-allowed=1;max-recv-level=e028", 0},
		{"missing_value", ";max-recv-level", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offer := SessionDescription{Type: "offer", SDP: "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=video 9 UDP/TLS/RTP/SAVPF 102\r\na=recvonly\r\na=rtpmap:102 H264/90000\r\na=fmtp:102 packetization-mode=1;profile-level-id=42e01f" + tc.params + "\r\n"}
			_, level, err := selectVideoCodec(offer)
			if tc.level == 0 {
				if err == nil {
					t.Fatalf("invalid fmtp accepted level=%d", level)
				}
				return
			}
			if err != nil || level != tc.level {
				t.Fatalf("level=%d err=%v want=%d", level, err, tc.level)
			}
			q := negotiateVideoQuality(fixtureSource(), level)
			wantWidth := 1280
			if tc.level == 40 {
				wantWidth = 1600
			}
			if q.Width != wantWidth {
				t.Fatalf("quality=%+v", q)
			}
		})
	}
}

func TestVideoCodecPionAnswerReceiveLevel(t *testing.T) {
	for _, asym := range []string{"", ";level-asymmetry-allowed=0", ";level-asymmetry-allowed=1"} {
		t.Run(asym, func(t *testing.T) {
			fmtp := "packetization-mode=1;profile-level-id=42e01f;max-recv-level=e028" + asym
			browser, err := newVideoPeerConnection(ICEConfig{}, fmtp)
			if err != nil {
				t.Fatal(err)
			}
			defer browser.Close()
			if _, err = browser.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
				t.Fatal(err)
			}
			offer, err := browser.CreateOffer(nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = browser.SetLocalDescription(offer); err != nil {
				t.Fatal(err)
			}
			m := NewRuntimeMirror(nil, time.Hour)
			defer m.Close(context.Background())
			m.SetCaptureHub(NewCaptureHub(&fixtureProvider{}))
			source := fixtureSource()
			n, err := m.AnswerVideo(context.Background(), "viewer", SessionDescriptionFromPion(offer), ICEConfig{}, source, fixtureGrant(source), 1)
			if err != nil {
				t.Fatal(err)
			}
			defer n.Abandon()
			if !strings.Contains(n.SDP, "a=fmtp:102 "+fmtp) {
				t.Fatalf("answer fmtp changed: %s", n.SDP)
			}
			want := uint8(31)
			if strings.HasSuffix(asym, "=1") {
				want = 40
			}
			if n.VideoQuality == nil || n.VideoQuality.MaxLevelIDC != want {
				t.Fatalf("quality=%+v want=%d", n.VideoQuality, want)
			}
			if err = browser.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: n.SDP}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVideoCodecRejectsUnsupportedProfileAndLevel(t *testing.T) {
	for _, profile := range []string{"42e0ff", "42e11f", "42001f", "640028", "4d8028", "42e00b", "42e01e", "42e0", "42e01f00", "invalid"} {
		t.Run(profile, func(t *testing.T) {
			offer := SessionDescription{Type: "offer", SDP: "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=video 9 UDP/TLS/RTP/SAVPF 102\r\na=recvonly\r\na=rtpmap:102 H264/90000\r\na=fmtp:102 packetization-mode=1;level-asymmetry-allowed=1;profile-level-id=" + profile + ";max-recv-level=e028\r\n"}
			if _, level, err := selectVideoCodec(offer); err == nil {
				t.Fatalf("profile=%s accepted level=%d", profile, level)
			}
		})
	}
}
