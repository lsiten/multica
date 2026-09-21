package daemon

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVoiceTranscriptionWithoutMirror(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	helper := filepath.Join(t.TempDir(), "transcriber")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n[ \"$MULTICA_VOICE_MIME\" = \"audio/webm\" ] || exit 1\n[ \"$(cat)\" = \"a\" ] || exit 1\nprintf 'recognized text'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	request := protocol.VoiceRequest{VoiceAudio: protocol.VoiceAudio{MimeType: "audio/webm", AudioBase64: "YQ=="}}
	result := transcribeVoiceRequest(context.Background(), request, helper)
	if result.Text != "recognized text" || result.Reason != "" {
		t.Fatalf("result=%+v", result)
	}
	result = transcribeVoiceRequest(context.Background(), request, "")
	if result.Reason != "transcriber_unavailable" {
		t.Fatal("missing config not reported")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := transcribeVoiceRequest(ctx, request, helper); r.Text != "" {
		t.Fatal("cancelled operation returned a transcript")
	}
}

func TestVoiceRuntimeScopeDoesNotRequireDisplay(t *testing.T) {
	d := &Daemon{
		workspaces:              map[string]*workspaceState{"ws": {runtimeIDs: []string{"rt"}}},
		runtimeIndex:            map[string]Runtime{"rt": {ID: "rt"}},
		mirrorControlGeneration: 1,
		vscreenServerGeneration: "generation",
	}
	e := protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "generation", RequestID: "request"}
	if !d.voiceEnvelopeCurrent(e, 1) {
		t.Fatal("valid voice scope required a display")
	}
	if d.voiceEnvelopeCurrent(e, 2) {
		t.Fatal("accepted stale connection")
	}
	e.WorkspaceID = "foreign"
	if d.voiceEnvelopeCurrent(e, 1) {
		t.Fatal("accepted foreign workspace")
	}
	e.WorkspaceID = "ws"
	e.RuntimeID = "foreign"
	if d.voiceEnvelopeCurrent(e, 1) {
		t.Fatal("accepted foreign runtime")
	}
}
