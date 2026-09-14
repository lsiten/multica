package mirror

import (
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestVideoQualityPreservesSelectedDisplay(t *testing.T) {
	_, offer := videoOffer(t, webrtc.RTPCodecTypeVideo)
	for _, tc := range []struct {
		name, profile         string
		wantWidth, wantHeight int
		wantLevel             uint8
	}{{"browser3.1", "42e01f", 1280, 720, 31}, {"level4", "42c028", 1600, 900, 40}} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := offer
			candidate.SDP = strings.ReplaceAll(candidate.SDP, "42c028", tc.profile)
			_, level, err := selectVideoCodec(candidate)
			if err != nil {
				t.Fatal(err)
			}
			source := fixtureSource()
			quality := negotiateVideoQuality(source, level)
			if quality.Binding != source.Binding || quality.DisplayID != source.DisplayID || quality.GeometryRevision != source.GeometryRevision {
				t.Fatal("quality negotiation changed selected source")
			}
			if quality.Width != tc.wantWidth || quality.Height != tc.wantHeight || quality.MaxLevelIDC != tc.wantLevel {
				t.Fatalf("negotiated quality=%+v", quality)
			}
		})
	}
	candidate := offer
	candidate.SDP = strings.ReplaceAll(candidate.SDP, "42c028", "42e01e")
	if _, _, err := selectVideoCodec(candidate); err == nil {
		t.Fatal("below supported level unexpectedly accepted")
	}
}
