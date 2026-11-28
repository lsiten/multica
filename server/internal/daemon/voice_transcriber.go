package daemon

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

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const maxVoiceTranscriptBytes = 16 * 1024

type transcriptBuffer struct{ bytes.Buffer }

func (b *transcriptBuffer) Write(value []byte) (int, error) {
	if len(value) > maxVoiceTranscriptBytes-b.Len() {
		return 0, errors.New("voice transcript exceeds limit")
	}
	return b.Buffer.Write(value)
}

type commandVoiceTranscriber struct{ executable string }

func newCommandVoiceTranscriber(executable string) (*commandVoiceTranscriber, error) {
	executable = strings.TrimSpace(executable)
	if executable == "" || strings.ContainsAny(executable, "\r\n") || !filepath.IsAbs(executable) {
		return nil, errors.New("voice transcriber executable is invalid")
	}
	return &commandVoiceTranscriber{executable: executable}, nil
}

func (t *commandVoiceTranscriber) Transcribe(ctx context.Context, request protocol.VoiceAudio, audio []byte) (string, error) {
	if t == nil || len(audio) == 0 {
		return "", errors.New("voice transcriber unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, t.executable)
	command.WaitDelay = time.Second
	command.Env = append(os.Environ(), "MULTICA_VOICE_MIME="+request.MimeType, "MULTICA_VOICE_LANGUAGE="+request.Language)
	command.Stdin = bytes.NewReader(audio)
	var output transcriptBuffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return "", err
	}
	transcript := strings.TrimSpace(output.String())
	if transcript == "" || !utf8.ValidString(transcript) {
		return "", errors.New("voice transcriber returned invalid text")
	}
	return transcript, nil
}
