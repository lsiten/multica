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

func TestVoiceTranscriptBufferRejectsOverflowWithoutRetainingIt(t *testing.T) {
	var output transcriptBuffer
	if _, err := output.Write(make([]byte, maxVoiceTranscriptBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("extra")); err == nil {
		t.Fatal("overflow was accepted")
	}
	if output.Len() > maxVoiceTranscriptBytes {
		t.Fatal("overflow was retained")
	}
}

func TestCommandVoiceTranscriberRejectsInvalidOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX helper")
	}
	for _, tc := range []struct{ name, script string }{
		{"oversize", "head -c 20000 /dev/zero"},
		{"invalid_utf8", "printf '\\377'"},
		{"empty", "printf '   '"},
		{"failed_process", "printf private; exit 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			helper := filepath.Join(t.TempDir(), "transcriber")
			if err := os.WriteFile(helper, []byte("#!/bin/sh\ncat >/dev/null\n"+tc.script+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			transcriber, err := NewCommandVoiceTranscriber(helper)
			if err != nil {
				t.Fatal(err)
			}
			text, err := transcriber.Transcribe(t.Context(), "audio/webm", []byte("audio"))
			if err == nil || text != "" {
				t.Fatalf("invalid helper output was accepted: error=%v", err)
			}
		})
	}
}
