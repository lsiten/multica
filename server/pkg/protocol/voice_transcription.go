package protocol

import (
	"encoding/base64"
	"errors"
	"mime"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DaemonCapabilityVoiceTranscriptionV1 = "voice-transcription-v1"
	EventVoiceTranscribe                 = "voice:transcribe"
	EventVoiceTranscript                 = "voice:transcript"
	EventVoiceCancel                     = "voice:cancel"
	MaxVoiceAudioBytes                   = 512 * 1024
	MaxVoiceRequestBytes                 = 720 * 1024
)

var ErrInvalidVoice = errors.New("invalid voice payload")

type VoiceAudio struct {
	MimeType    string `json:"mime_type"`
	AudioBase64 string `json:"audio_base64"`
}

func (v VoiceAudio) Decode() ([]byte, error) {
	mediaType, _, err := mime.ParseMediaType(v.MimeType)
	if err != nil || len(v.MimeType) > 128 {
		return nil, ErrInvalidVoice
	}
	switch mediaType {
	case "audio/webm", "audio/ogg", "audio/mp4", "audio/wav", "audio/x-wav":
	default:
		return nil, ErrInvalidVoice
	}
	if len(v.AudioBase64) > base64.StdEncoding.EncodedLen(MaxVoiceAudioBytes) {
		return nil, ErrInvalidVoice
	}
	audio, err := base64.StdEncoding.DecodeString(v.AudioBase64)
	if err != nil || len(audio) == 0 || len(audio) > MaxVoiceAudioBytes {
		return nil, ErrInvalidVoice
	}
	return audio, nil
}

// VoiceRequest uses the existing connection-scoped envelope but does not need a display or mirror session.
type VoiceRequest struct {
	VscreenEnvelope
	VoiceAudio
	Deadline time.Time `json:"deadline"`
}

type VoiceResult struct {
	VscreenEnvelope
	Text   string `json:"text,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func (v VoiceResult) Valid() bool {
	if v.VscreenEnvelope.Validate() != nil {
		return false
	}
	if v.Reason == "" {
		return len(v.Text) <= 16*1024 && strings.TrimSpace(v.Text) != "" && utf8.ValidString(v.Text)
	}
	if v.Text != "" {
		return false
	}
	switch v.Reason {
	case "transcriber_unavailable", "transcription_failed", "invalid_audio", "busy", "timeout":
		return true
	default:
		return false
	}
}
