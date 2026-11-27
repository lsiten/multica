package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func desktopVoiceFixture(t *testing.T, text string) (string, func()) {
	t.Helper()
	token := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/transcribe" || r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(403)
			return
		}
		var audio protocol.VoiceAudio
		if json.NewDecoder(r.Body).Decode(&audio) != nil || audio.MimeType != "audio/mp4" || audio.AudioBase64 != "YQ==" {
			w.WriteHeader(400)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]string{"text": text}); err != nil {
			t.Error(err)
		}
	}))
	descriptor, err := json.Marshal(desktopVoiceDescriptor{URL: server.URL + "/transcribe", Token: token})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "voice-bridge.json")
	if err := os.WriteFile(path, descriptor, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return path, server.Close
}

func voiceDesktopRequest() protocol.VoiceRequest {
	return protocol.VoiceRequest{RuntimeEnvelope: protocol.RuntimeEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "gen", RequestID: "req"}, VoiceAudio: protocol.VoiceAudio{MimeType: "audio/mp4", AudioBase64: "YQ=="}}
}

func TestDesktopVoiceUsesCurrentDescriptorAfterDesktopRestart(t *testing.T) {
	path, closeFirst := desktopVoiceFixture(t, "first")
	next, _ := desktopVoiceFixture(t, "second")
	t.Setenv("MULTICA_DESKTOP_VOICE_DESCRIPTOR", path)
	t.Setenv("MULTICA_VOICE_TRANSCRIBER", "")
	d := &Daemon{cfg: Config{LaunchedBy: "desktop"}}
	if result := d.transcribeVoice(context.Background(), voiceDesktopRequest()); result.Text != "first" || result.Reason != "" {
		t.Fatalf("first result=%+v", result)
	}
	closeFirst()
	data, err := os.ReadFile(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if result := d.transcribeVoice(context.Background(), voiceDesktopRequest()); result.Text != "second" || result.Reason != "" {
		t.Fatalf("restarted result=%+v", result)
	}
}

func TestDesktopVoiceDoesNotOverrideExplicitTranscriberOrStandaloneRuntime(t *testing.T) {
	path, _ := desktopVoiceFixture(t, "must not use desktop")
	t.Setenv("MULTICA_DESKTOP_VOICE_DESCRIPTOR", path)
	for _, tc := range []struct{ launchedBy, command string }{{"", ""}, {"desktop", "invalid-relative-command"}} {
		t.Run(tc.launchedBy+tc.command, func(t *testing.T) {
			t.Setenv("MULTICA_VOICE_TRANSCRIBER", tc.command)
			d := &Daemon{cfg: Config{LaunchedBy: tc.launchedBy}}
			result := d.transcribeVoice(context.Background(), voiceDesktopRequest())
			if result.Reason != "transcriber_unavailable" || result.Text != "" {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestDesktopVoiceRejectsEmptyRecognition(t *testing.T) {
	path, _ := desktopVoiceFixture(t, " ")
	result := transcribeDesktopVoice(context.Background(), voiceDesktopRequest(), path)
	if result.Reason != "transcription_failed" || result.Text != "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestDesktopVoiceRejectsNonLoopbackDescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voice-bridge.json")
	for _, endpoint := range []string{"http://example.com:80/transcribe", "http://127.0.0.1:80/other", "https://127.0.0.1:80/transcribe"} {
		data, err := json.Marshal(desktopVoiceDescriptor{URL: endpoint, Token: strings.Repeat("a", 64)})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readDesktopVoiceDescriptor(path); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
}
