package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type desktopVoiceDescriptor struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func (d *Daemon) transcribeVoice(ctx context.Context, request protocol.VoiceRequest) protocol.VoiceResult {
	executable := os.Getenv("MULTICA_VOICE_TRANSCRIBER")
	if strings.TrimSpace(executable) != "" || d.cfg.LaunchedBy != "desktop" {
		return transcribeVoiceRequest(ctx, request, executable)
	}
	path := os.Getenv("MULTICA_DESKTOP_VOICE_DESCRIPTOR")
	if path == "" {
		root, err := os.UserConfigDir()
		if err != nil {
			return protocol.VoiceResult{RuntimeEnvelope: request.RuntimeEnvelope, Reason: "transcriber_unavailable"}
		}
		path = filepath.Join(root, "Multica", "voice-bridge.json")
	}
	return transcribeDesktopVoice(ctx, request, path)
}

func transcribeDesktopVoice(ctx context.Context, request protocol.VoiceRequest, path string) protocol.VoiceResult {
	result := protocol.VoiceResult{RuntimeEnvelope: request.RuntimeEnvelope}
	if _, err := request.VoiceAudio.Decode(); err != nil {
		result.Reason = "invalid_audio"
		return result
	}
	descriptor, err := readDesktopVoiceDescriptor(path)
	if err != nil {
		result.Reason = "transcriber_unavailable"
		return result
	}
	body, err := json.Marshal(request.VoiceAudio)
	if err != nil {
		result.Reason = "invalid_audio"
		return result
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, descriptor.URL, bytes.NewReader(body))
	if err != nil {
		result.Reason = "transcriber_unavailable"
		return result
	}
	req.Header.Set("Authorization", "Bearer "+descriptor.Token)
	req.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		result.Reason = "transcription_failed"
		return result
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		result.Reason = "transcription_failed"
		var failure struct {
			Error string `json:"error"`
		}
		if response.StatusCode == http.StatusServiceUnavailable && json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&failure) == nil {
			switch failure.Error {
			case "no_speech", "unreliable_transcript":
				result.Reason = failure.Error
			}
		}
		return result
	}
	var transcript struct {
		Text string `json:"text"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxVoiceTranscriptBytes*6+1024))
	if err := decoder.Decode(&transcript); err != nil {
		result.Reason = "transcription_failed"
		return result
	}
	result.Text = strings.TrimSpace(transcript.Text)
	if !result.Valid() {
		result.Text = ""
		result.Reason = "transcription_failed"
	}
	return result
}

// Read for every request so a retained daemon follows a restarted desktop's port
// and authentication token without interrupting its running agent tasks.
func readDesktopVoiceDescriptor(path string) (desktopVoiceDescriptor, error) {
	var descriptor desktopVoiceDescriptor
	file, err := os.Open(path)
	if err != nil {
		return descriptor, err
	}
	defer file.Close()
	if err := json.NewDecoder(io.LimitReader(file, 4096)).Decode(&descriptor); err != nil {
		return descriptor, err
	}
	endpoint, err := url.Parse(descriptor.URL)
	if err != nil {
		return descriptor, err
	}
	if endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.Port() == "" || endpoint.Path != "/transcribe" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || len(descriptor.Token) != 64 || strings.ContainsAny(descriptor.Token, "\r\n") {
		return descriptor, errors.New("invalid desktop voice bridge descriptor")
	}
	return descriptor, nil
}
