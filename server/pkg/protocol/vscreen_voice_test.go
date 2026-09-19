package protocol

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestParseMirrorVoiceMessageBoundsAndDecodesAudio(t *testing.T) {
	message := MirrorVoiceMessage{
		Type: MirrorVoiceAudio, GrantID: "grant", Seq: 1, MimeType: "audio/webm",
		AudioBase64: base64.StdEncoding.EncodeToString([]byte("audio")),
	}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	parsed, audio, err := ParseMirrorVoiceMessage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Seq != 1 || string(audio) != "audio" {
		t.Fatalf("parsed voice = seq %d, audio %q", parsed.Seq, audio)
	}
}

func TestParseMirrorVoiceMessageRejectsOversizedAudio(t *testing.T) {
	message := MirrorVoiceMessage{
		Type: MirrorVoiceAudio, GrantID: "grant", Seq: 1, MimeType: "audio/webm",
		AudioBase64: base64.StdEncoding.EncodeToString(make([]byte, MaxMirrorVoiceBytes+1)),
	}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseMirrorVoiceMessage(raw); err == nil {
		t.Fatal("oversized voice payload was accepted")
	}
}
