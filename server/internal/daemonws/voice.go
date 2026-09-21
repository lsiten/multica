package daemonws

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type voicePending struct {
	client   *client
	envelope protocol.VscreenEnvelope
	result   chan protocol.VoiceResult
}

// TranscribeVoice only holds correlation state in memory for the lifetime of the HTTP request.
func (h *Hub) TranscribeVoice(ctx context.Context, workspaceID, runtimeID, daemonID string, audio protocol.VoiceAudio) (protocol.VoiceResult, error) {
	if _, err := audio.Decode(); err != nil {
		return protocol.VoiceResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	h.mu.Lock()
	c := h.vscreenClientLocked(workspaceID, runtimeID, daemonID)
	if c == nil {
		h.mu.Unlock()
		return protocol.VoiceResult{}, ErrVscreenUnavailable
	}
	count := 0
	for _, p := range h.voicePending {
		if p.client == c {
			count++
		}
	}
	if len(h.voicePending) >= 32 || count >= 2 {
		h.mu.Unlock()
		return protocol.VoiceResult{}, ErrVscreenCapacity
	}
	e := protocol.VscreenEnvelope{WorkspaceID: workspaceID, RuntimeID: runtimeID, DaemonGeneration: c.vscreenGeneration, RequestID: uuid.NewString()}
	p := &voicePending{client: c, envelope: e, result: make(chan protocol.VoiceResult, 1)}
	if h.voicePending == nil {
		h.voicePending = make(map[string]*voicePending)
	}
	h.voicePending[e.RequestID] = p
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.voicePending, e.RequestID); h.mu.Unlock() }()
	deadline, _ := ctx.Deadline()
	frame := mustMarshalRaw(protocol.Message{Type: protocol.EventVoiceTranscribe, Payload: mustMarshalRaw(protocol.VoiceRequest{VscreenEnvelope: e, VoiceAudio: audio, Deadline: deadline})})
	if !c.trySend(frame) {
		return protocol.VoiceResult{}, ErrVscreenUnavailable
	}
	select {
	case result := <-p.result:
		return result, nil
	case <-ctx.Done():
		c.trySend(mustMarshalRaw(protocol.Message{Type: protocol.EventVoiceCancel, Payload: mustMarshalRaw(e)}))
		return protocol.VoiceResult{}, ctx.Err()
	case <-c.ctx.Done():
		return protocol.VoiceResult{}, ErrVscreenUnavailable
	}
}

func (c *client) handleVoiceResult(raw json.RawMessage) {
	if len(raw) > 128*1024 {
		return
	}
	var result protocol.VoiceResult
	if json.Unmarshal(raw, &result) != nil || !result.Valid() {
		return
	}
	h := c.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.voicePending[result.RequestID]
	if p == nil || p.client != c || p.envelope != result.VscreenEnvelope || h.vscreenClientLocked(result.WorkspaceID, result.RuntimeID, c.identity.DaemonID) != c {
		return
	}
	select {
	case p.result <- result:
	default:
	}
}
