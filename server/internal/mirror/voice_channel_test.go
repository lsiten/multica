package mirror

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type transcriptionFixture func(context.Context, string, []byte) (string, error)

func (f transcriptionFixture) Transcribe(ctx context.Context, mime string, audio []byte) (string, error) {
	return f(ctx, mime, audio)
}

func TestVoiceChecksCapabilityBeforeAndAfterTranscription(t *testing.T) {
	for _, scenario := range []string{"accepted", "expired", "revoked_during_transcription", "cancelled", "replayed"} {
		t.Run(scenario, func(t *testing.T) {
			m := NewRuntimeMirror(nil, time.Hour)
			peer := &mirrorPeer{controlGrant: &controlGrant{
				value:    testControlGrant("voice-grant", "display", time.Now().Add(time.Hour)),
				deadline: time.Now().Add(time.Minute),
			}}
			calls := 0
			m.SetVoiceTranscriber(transcriptionFixture(func(ctx context.Context, mime string, audio []byte) (string, error) {
				calls++
				if mime != "audio/webm" || string(audio) != "fixture" {
					t.Fatal("transcriber received a changed recording")
				}
				if scenario == "revoked_during_transcription" {
					peer.mu.Lock()
					peer.controlGrant = nil
					peer.mu.Unlock()
				}
				return "transcript", nil
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "expired" {
				peer.controlGrant.deadline = time.Now().Add(-time.Second)
			}
			if scenario == "cancelled" {
				cancel()
			}
			if scenario == "replayed" {
				peer.controlGrant.voiceSeq = 1
			}
			payload, err := json.Marshal(protocol.MirrorVoiceMessage{
				Type: protocol.MirrorVoiceAudio, GrantID: "voice-grant", Seq: 1,
				MimeType: "audio/webm", AudioBase64: base64.StdEncoding.EncodeToString([]byte("fixture")),
			})
			if err != nil {
				t.Fatal(err)
			}
			result := m.transcribeVoice(ctx, peer, payload)
			if scenario == "accepted" {
				if result.Text != "transcript" || result.Reason != "" || result.Seq != 1 {
					t.Fatalf("valid transcription failed: %+v", result)
				}
				return
			}
			if result.Text != "" || result.Reason == "" {
				t.Fatal("invalid capability returned a transcript")
			}
			if scenario != "revoked_during_transcription" && calls != 0 {
				t.Fatal("invalid recording started transcription")
			}
		})
	}
}
