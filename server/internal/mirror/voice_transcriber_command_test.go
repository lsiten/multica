package mirror

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCommandVoiceTranscriberKeepsAudioOnLocalStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX helper")
	}
	dir := t.TempDir()
	helper := filepath.Join(dir, "transcriber")
	script := "#!/bin/sh\ncat >/dev/null\nprintf ' local transcript  '\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	transcriber, err := NewCommandVoiceTranscriber(helper)
	if err != nil {
		t.Fatal(err)
	}
	got, err := transcriber.Transcribe(context.Background(), "audio/webm", []byte("private audio"))
	if err != nil || got != "local transcript" {
		t.Fatalf("transcript = %q, err = %v", got, err)
	}
}

func TestCommandVoiceTranscriberRequiresAbsoluteExecutable(t *testing.T) {
	if _, err := NewCommandVoiceTranscriber("transcriber"); err == nil {
		t.Fatal("expected relative executable to be rejected")
	}
}
