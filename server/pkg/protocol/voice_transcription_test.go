package protocol

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestVoiceAudioValidation(t *testing.T) {
	for _, tc := range []struct {
		name, mime, audio string
		valid             bool
	}{
		{"webm", "audio/webm;codecs=opus", base64.StdEncoding.EncodeToString([]byte("audio")), true},
		{"empty", "audio/webm", "", false},
		{"invalid base64", "audio/webm", "???", false},
		{"wrong format", "text/html", "YQ==", false},
		{"too large", "audio/webm", base64.StdEncoding.EncodeToString(make([]byte, MaxVoiceAudioBytes+1)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (VoiceAudio{MimeType: tc.mime, AudioBase64: tc.audio}).Decode()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestVoiceResultValidation(t *testing.T) {
	e := RuntimeEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "gen", RequestID: "req"}
	for _, r := range []VoiceResult{{RuntimeEnvelope: e}, {RuntimeEnvelope: e, Text: "  "}, {RuntimeEnvelope: e, Text: strings.Repeat("a", 16385)}, {RuntimeEnvelope: e, Text: "secret", Reason: "busy"}, {RuntimeEnvelope: e, Reason: "unknown"}} {
		if r.Valid() {
			t.Fatal("accepted invalid response")
		}
	}
	if !(VoiceResult{RuntimeEnvelope: e, Text: "transcript"}).Valid() {
		t.Fatal("rejected valid transcript")
	}
}

func TestVoiceAudioLanguageAllowsUILocalesAndLegacyRequests(t *testing.T) {
	for _, language := range []string{"", "en", "zh", "zh-Hans", "zh-Hant", "es", "fr", "ja", "ko", "ru"} {
		if _, err := (VoiceAudio{MimeType: "audio/mp4", AudioBase64: "YQ==", Language: language}).Decode(); err != nil {
			t.Fatalf("language %q: %v", language, err)
		}
	}
	for _, language := range []string{"auto", "de", "zh-CN", "en\nMULTICA_TOKEN=x"} {
		if _, err := (VoiceAudio{MimeType: "audio/mp4", AudioBase64: "YQ==", Language: language}).Decode(); err == nil {
			t.Fatalf("accepted invalid language %q", language)
		}
	}
}
