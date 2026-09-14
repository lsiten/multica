package mirror

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/pion/interceptor"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

// This matches the native ConstrainedBaseline encoder at 1600x900@30 (level 4).
const videoH264Fmtp = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42c028"

func newVideoPeerConnection(ice ICEConfig, preferred ...string) (*webrtc.PeerConnection, error) {
	fmtp := videoH264Fmtp
	if len(preferred) > 0 {
		fmtp = preferred[0]
	}
	engine := &webrtc.MediaEngine{}
	err := engine.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: fmtp, RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "ccm", Parameter: "fir"}}}, PayloadType: 102}, webrtc.RTPCodecTypeVideo)
	if err != nil {
		return nil, fmt.Errorf("mirror: register h264: %w", err)
	}
	registry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(engine, registry); err != nil {
		return nil, fmt.Errorf("mirror: video interceptors: %w", err)
	}
	return webrtc.NewAPI(webrtc.WithMediaEngine(engine), webrtc.WithInterceptorRegistry(registry)).NewPeerConnection(ice.Pion())
}

func selectVideoCodec(offer SessionDescription) (string, uint8, error) {
	var description sdp.SessionDescription
	if err := description.Unmarshal([]byte(offer.SDP)); err != nil {
		return "", 0, fmt.Errorf("%w: malformed video sdp: %v", ErrInvalidOffer, err)
	}
	var selected string
	var selectedLevel uint8
	for _, section := range description.MediaDescriptions {
		if section.MediaName.Media != "video" || section.MediaName.Port.Value == 0 {
			continue
		}
		_, inactive := section.Attribute("inactive")
		_, sendonly := section.Attribute("sendonly")
		if inactive || sendonly {
			continue
		}
		h264 := make(map[string]bool)
		for _, attribute := range section.Attributes {
			if attribute.Key == "rtpmap" {
				fields := strings.Fields(attribute.Value)
				if len(fields) == 2 && strings.EqualFold(fields[1], "H264/90000") {
					h264[fields[0]] = true
				}
			}
		}
		for _, attribute := range section.Attributes {
			if attribute.Key != "fmtp" {
				continue
			}
			fields := strings.SplitN(attribute.Value, " ", 2)
			if len(fields) != 2 || !h264[fields[0]] {
				continue
			}
			parameters := make(map[string]string)
			for _, part := range strings.Split(fields[1], ";") {
				pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
				if len(pair) == 2 {
					parameters[pair[0]] = pair[1]
				}
			}
			profile, err := hex.DecodeString(parameters["profile-level-id"])
			if err == nil && len(profile) == 3 && profile[0] == 0x42 && profile[1]&0x40 != 0 && profile[2] >= 31 && parameters["packetization-mode"] == "1" {
				level := profile[2]
				if maximum, err := hex.DecodeString(parameters["max-recv-level"]); err == nil && len(maximum) == 2 && maximum[1] > level {
					level = maximum[1]
				}
				if level > selectedLevel {
					selected, selectedLevel = fields[1], level
				}
			}
		}
	}
	if selected != "" {
		return selected, selectedLevel, nil
	}
	return "", 0, errors.New("mirror: offer requires receiving constrained baseline h264 level 3.1 or higher with packetization mode 1")
}
