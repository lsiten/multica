package mirror

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const maxVoiceTranscriptBytes = 16 * 1024

type transcriptBuffer struct {
	buffer bytes.Buffer
}

func (b *transcriptBuffer) Len() int       { return b.buffer.Len() }
func (b *transcriptBuffer) String() string { return b.buffer.String() }

func (b *transcriptBuffer) Write(value []byte) (int, error) {
	if len(value) > maxVoiceTranscriptBytes-b.Len() {
		return 0, errors.New("mirror: voice transcript exceeds limit")
	}
	return b.buffer.Write(value)
}

// CommandVoiceTranscriber runs a locally installed speech-to-text helper.
// Audio is written only to the helper's stdin; stdout is treated as the
// transcript and neither stream is logged or persisted by the daemon.
type CommandVoiceTranscriber struct {
	executable string
}

func NewCommandVoiceTranscriber(executable string) (*CommandVoiceTranscriber, error) {
	executable = strings.TrimSpace(executable)
	if executable == "" || strings.ContainsAny(executable, "\r\n") {
		return nil, errors.New("mirror: voice transcriber executable is empty or invalid")
	}
	if !filepath.IsAbs(executable) {
		return nil, errors.New("mirror: voice transcriber executable must be an absolute path")
	}
	return &CommandVoiceTranscriber{executable: executable}, nil
}

func (t *CommandVoiceTranscriber) Transcribe(ctx context.Context, mimeType string, audio []byte) (string, error) {
	if t == nil || t.executable == "" || len(audio) == 0 {
		return "", errors.New("mirror: voice transcriber unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, t.executable)
	command.WaitDelay = time.Second
	command.Env = append(os.Environ(), "MULTICA_VOICE_MIME="+mimeType)
	command.Stdin = bytes.NewReader(audio)
	var output transcriptBuffer
	command.Stdout = &output
	command.Stderr = nil
	if err := command.Run(); err != nil {
		return "", err
	}
	transcript := strings.TrimSpace(output.String())
	if transcript == "" || !utf8.ValidString(transcript) {
		return "", errors.New("mirror: voice transcriber returned invalid text")
	}
	return transcript, nil
}
