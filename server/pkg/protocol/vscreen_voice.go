package protocol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// MirrorVoiceMessage is sent over the P2P mirror-voice data channel. Audio is
// intentionally never routed through the API server; the daemon decodes it and
// passes it to its local speech-to-text provider.
type MirrorVoiceMessage struct {
	Type        string `json:"type"`
	GrantID     string `json:"grant_id"`
	Seq         uint64 `json:"seq"`
	MimeType    string `json:"mime_type"`
	AudioBase64 string `json:"audio_base64"`
}

const (
	MirrorVoiceAudio      = "mirror-voice:audio"
	MirrorVoiceTranscript = "mirror-voice:transcript"
	MaxMirrorVoiceBytes   = 512 * 1024
)

func (m MirrorVoiceMessage) Audio() ([]byte, error) {
	if m.Type != MirrorVoiceAudio || !vscreenIdentity(m.GrantID) || m.Seq == 0 || m.MimeType == "" || m.AudioBase64 == "" {
		return nil, fmt.Errorf("%w: invalid voice message", ErrInvalidMirrorDescription)
	}
	audio, err := base64.StdEncoding.DecodeString(m.AudioBase64)
	if err != nil || len(audio) == 0 || len(audio) > MaxMirrorVoiceBytes {
		return nil, fmt.Errorf("%w: invalid voice payload", ErrInvalidMirrorDescription)
	}
	return audio, nil
}

func ParseMirrorVoiceMessage(raw []byte) (MirrorVoiceMessage, []byte, error) {
	if len(raw) == 0 || len(raw) > MaxMirrorInputBytes*64 {
		return MirrorVoiceMessage{}, nil, fmt.Errorf("%w: voice message size", ErrInvalidMirrorDescription)
	}
	var message MirrorVoiceMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return MirrorVoiceMessage{}, nil, fmt.Errorf("%w: decode voice message: %v", ErrInvalidMirrorDescription, err)
	}
	audio, err := message.Audio()
	if err != nil {
		return MirrorVoiceMessage{}, nil, err
	}
	return message, audio, nil
}
