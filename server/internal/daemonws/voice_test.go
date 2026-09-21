package daemonws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVoiceRelayRoundTripAndCleanup(t *testing.T) {
	h := NewHub()
	c := dialVscreenTestConn(t, h, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan protocol.VoiceResult, 1)
	go func() {
		r, err := h.TranscribeVoice(ctx, "ws", "rt", "daemon", protocol.VoiceAudio{MimeType: "audio/webm", AudioBase64: "YQ=="})
		if err != nil {
			r.Reason = err.Error()
		}
		done <- r
	}()
	var frame protocol.Message
	if err := c.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	var req protocol.VoiceRequest
	if err := json.Unmarshal(frame.Payload, &req); err != nil {
		t.Fatal(err)
	}
	if frame.Type != protocol.EventVoiceTranscribe || req.AudioBase64 != "YQ==" {
		t.Fatal("incorrect relay payload")
	}
	if err := c.WriteJSON(protocol.Message{Type: protocol.EventVoiceTranscript, Payload: mustMarshalRaw(protocol.VoiceResult{VscreenEnvelope: req.VscreenEnvelope, Text: "hello"})}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.Text != "hello" {
			t.Fatalf("result=%+v", r)
		}
	case <-ctx.Done():
		t.Fatal("relay did not finish")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.voicePending) != 0 {
		t.Fatal("request retained after completion")
	}
}

func TestVoiceRelayRejectsForeignReplyAndCancels(t *testing.T) {
	h := NewHub()
	c := dialVscreenTestConn(t, h, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	foreign := dialVscreenTestConn(t, h, ClientIdentity{DaemonID: "other", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := h.TranscribeVoice(ctx, "ws", "rt", "daemon", protocol.VoiceAudio{MimeType: "audio/webm", AudioBase64: "YQ=="})
		done <- err
	}()
	var frame protocol.Message
	if err := c.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	var req protocol.VoiceRequest
	if err := json.Unmarshal(frame.Payload, &req); err != nil {
		t.Fatal(err)
	}
	if err := foreign.WriteJSON(protocol.Message{Type: protocol.EventVoiceTranscript, Payload: mustMarshalRaw(protocol.VoiceResult{VscreenEnvelope: req.VscreenEnvelope, Text: "forged"})}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatalf("foreign response completed request: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not finish")
	}
	if err := c.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != protocol.EventVoiceCancel {
		t.Fatal("cancel was not forwarded")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.voicePending) != 0 {
		t.Fatal("request retained after cancellation")
	}
}

func TestVoiceRelayBoundsConcurrencyAndReleasesOnDisconnect(t *testing.T) {
	h := NewHub()
	c := dialVscreenTestConn(t, h, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 2)
	audio := protocol.VoiceAudio{MimeType: "audio/webm", AudioBase64: "YQ=="}
	for range 2 {
		go func() { _, err := h.TranscribeVoice(ctx, "ws", "rt", "daemon", audio); done <- err }()
		var frame protocol.Message
		if err := c.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.TranscribeVoice(ctx, "ws", "rt", "daemon", audio); !errors.Is(err, ErrVscreenCapacity) {
		t.Fatalf("expected capacity limit, got %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err := <-done:
			if !errors.Is(err, ErrVscreenUnavailable) {
				t.Fatalf("disconnect: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("disconnect did not cancel requests")
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.voicePending) != 0 {
		t.Fatal("retained requests after disconnect")
	}
}
