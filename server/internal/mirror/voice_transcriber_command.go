package mirror

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

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
	if !strings.HasPrefix(executable, "/") {
		return nil, errors.New("mirror: voice transcriber executable must be an absolute path")
	}
	return &CommandVoiceTranscriber{executable: executable}, nil
}

func (t *CommandVoiceTranscriber) Transcribe(ctx context.Context, mimeType string, audio []byte) (string, error) {
	if t == nil || t.executable == "" || len(audio) == 0 {
		return "", errors.New("mirror: voice transcriber unavailable")
	}
	command := exec.CommandContext(ctx, t.executable)
	command.Env = append(os.Environ(), "MULTICA_VOICE_MIME="+mimeType)
	command.Stdin = bytes.NewReader(audio)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = nil
	if err := command.Run(); err != nil {
		return "", err
	}
	transcript := strings.TrimSpace(output.String())
	if transcript == "" || len(transcript) > 16*1024 {
		return "", errors.New("mirror: voice transcriber returned invalid text")
	}
	return transcript, nil
}
