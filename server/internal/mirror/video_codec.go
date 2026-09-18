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
			level, ok := videoReceiveLevel(fields[1])
			if ok && level > selectedLevel {
				selected, selectedLevel = fields[1], level
			}
		}
	}
	if selected != "" {
		return selected, selectedLevel, nil
	}
	return "", 0, errors.New("mirror: offer requires receiving constrained baseline h264 level 3.1 or higher with packetization mode 1")
}

// The selected fmtp is also registered locally and retained in Pion's answer.
// Consequently asymmetry is bilateral only when this parameter is exactly 1.
func videoReceiveLevel(fmtp string) (uint8, bool) {
	parameters := make(map[string]string)
	for _, part := range strings.Split(fmtp, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || value == "" {
			return 0, false
		}
		if _, exists := parameters[key]; exists {
			return 0, false
		}
		parameters[key] = value
	}
	profile, err := hex.DecodeString(parameters["profile-level-id"])
	if err != nil || len(profile) != 3 || profile[0] != 0x42 || profile[1]&0x4f != 0x40 || !videoKnownLevel(profile[2]) || parameters["packetization-mode"] != "1" {
		return 0, false
	}
	asymmetry := parameters["level-asymmetry-allowed"]
	if asymmetry != "" && asymmetry != "0" && asymmetry != "1" {
		return 0, false
	}
	level := profile[2]
	if value, present := parameters["max-recv-level"]; present {
		maximum, err := hex.DecodeString(value)
		if err != nil || len(maximum) != 2 || maximum[0] != profile[1] || !videoKnownLevel(maximum[1]) || maximum[1] <= level {
			return 0, false
		}
		if asymmetry == "1" {
			level = maximum[1]
		}
	}
	return level, true
}

// Levels below 3.1 are outside this encoder path, including the special 1b encoding.
func videoKnownLevel(level uint8) bool {
	switch level {
	case 31, 32, 40, 41, 42, 50, 51, 52, 60, 61, 62:
		return true
	default:
		return false
	}
}
