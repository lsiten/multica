package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// voiceRequests is owned by one websocket reader; closing it cancels and joins all helpers.
type voiceRequests struct {
	ctx    context.Context
	mu     sync.Mutex
	active map[string]context.CancelFunc
	wg     sync.WaitGroup
	d      *Daemon
	reader taskWakeupReader
}

func (d *Daemon) voiceEnvelopeCurrent(e protocol.RuntimeEnvelope, generation mirrorConnectionGeneration) bool {
	if e.Validate() != nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if generation == 0 || generation != d.mirrorConnectionGeneration || e.DaemonGeneration != d.vscreenServerGeneration {
		return false
	}
	if _, ok := d.runtimeIndex[e.RuntimeID]; !ok {
		return false
	}
	if workspace := d.workspaces[e.WorkspaceID]; workspace != nil {
		for _, id := range workspace.runtimeIDs {
			if id == e.RuntimeID {
				return true
			}
		}
	}
	return false
}

func (v *voiceRequests) close() {
	v.mu.Lock()
	for _, cancel := range v.active {
		cancel()
	}
	v.mu.Unlock()
	v.wg.Wait()
}

func (v *voiceRequests) cancel(raw json.RawMessage) {
	var e protocol.RuntimeEnvelope
	if json.Unmarshal(raw, &e) != nil || !v.d.voiceEnvelopeCurrent(e, v.reader.mirrorConnectionGeneration) {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if cancel := v.active[e.RequestID]; cancel != nil {
		cancel()
	}
}

func (v *voiceRequests) handle(raw json.RawMessage) {
	if len(raw) > protocol.MaxVoiceRequestBytes {
		return
	}
	var request protocol.VoiceRequest
	if json.Unmarshal(raw, &request) != nil || !v.d.voiceEnvelopeCurrent(request.RuntimeEnvelope, v.reader.mirrorConnectionGeneration) {
		return
	}
	v.mu.Lock()
	if _, exists := v.active[request.RequestID]; exists {
		v.mu.Unlock()
		return
	}
	if len(v.active) >= 2 {
		v.mu.Unlock()
		v.reply(protocol.VoiceResult{RuntimeEnvelope: request.RuntimeEnvelope, Reason: "busy"})
		return
	}
	deadline := request.Deadline
	if max := time.Now().Add(25 * time.Second); deadline.After(max) {
		deadline = max
	}
	ctx, cancel := context.WithDeadline(v.ctx, deadline)
	if v.active == nil {
		v.active = make(map[string]context.CancelFunc)
	}
	v.active[request.RequestID] = cancel
	v.wg.Add(1)
	v.mu.Unlock()
	go func() {
		defer v.wg.Done()
		defer cancel()
		defer func() { v.mu.Lock(); delete(v.active, request.RequestID); v.mu.Unlock() }()
		result := v.d.transcribeVoice(ctx, request)
		if ctx.Err() == nil {
			v.reply(result)
		}
	}()
}

func (v *voiceRequests) reply(result protocol.VoiceResult) {
	frame := marshalRaw(protocol.Message{Type: protocol.EventVoiceTranscript, Payload: marshalRaw(result)})
	if _, err := v.reader.enqueue(frame); err != nil {
		v.d.logger.Debug("voice transcription reply dropped")
	}
}

func transcribeVoiceRequest(ctx context.Context, request protocol.VoiceRequest, executable string) protocol.VoiceResult {
	result := protocol.VoiceResult{RuntimeEnvelope: request.RuntimeEnvelope}
	audio, err := request.VoiceAudio.Decode()
	if err != nil {
		result.Reason = "invalid_audio"
		return result
	}
	transcriber, err := newCommandVoiceTranscriber(executable)
	if err != nil {
		result.Reason = "transcriber_unavailable"
		return result
	}
	text, err := transcriber.Transcribe(ctx, request.MimeType, audio)
	if err != nil {
		result.Reason = "transcription_failed"
		return result
	}
	result.Text = text
	return result
}
