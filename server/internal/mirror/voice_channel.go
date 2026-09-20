package mirror

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

// bindVoiceChannel reassembles bounded recordings from a viewer. The
// channel is gated by the authenticated viewing capability and never writes the
// audio to logs, storage, or signaling messages.
func (m *RuntimeMirror) bindVoiceChannel(viewerID string, peer *mirrorPeer, channel *webrtc.DataChannel) {
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var assembly voiceAssembly
	channel.OnClose(func() {
		cancel()
		mu.Lock()
		assembly = voiceAssembly{}
		mu.Unlock()
	})
	channel.OnMessage(func(message webrtc.DataChannelMessage) {
		if message.IsString || ctx.Err() != nil {
			return
		}
		peer.mu.Lock()
		grant := peer.grant
		valid := !peer.closed && grant != nil && time.Now().Before(grant.deadline) && time.Now().Before(grant.value.ExpiresAt)
		peer.mu.Unlock()
		mu.Lock()
		if !valid {
			assembly = voiceAssembly{}
			mu.Unlock()
			return
		}
		recording, packetErr := assembly.push(message.Data, time.Now())
		mu.Unlock()
		if packetErr == nil && recording == nil {
			return
		}
		result := voiceResult{Type: "mirror-voice:error", Reason: "invalid_audio"}
		if packetErr == nil {
			result = m.transcribeVoice(ctx, peer, recording)
		}
		payload, err := json.Marshal(result)
		if err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
		if err := channel.SendText(string(payload)); err != nil {
			cancel()
		}
	})
}

type voiceResult struct {
	Type   string `json:"type"`
	Seq    uint64 `json:"seq"`
	Text   string `json:"text,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func (m *RuntimeMirror) transcribeVoice(ctx context.Context, peer *mirrorPeer, raw []byte) voiceResult {
	result := voiceResult{Type: "mirror-voice:error", Reason: "invalid_audio"}
	voice, audio, err := protocol.ParseMirrorVoiceMessage(raw)
	if err != nil {
		return result
	}
	result.Seq = voice.Seq
	result.Reason = "denied"
	if ctx.Err() != nil {
		return result
	}
	peer.mu.Lock()
	grant := peer.grant
	valid := !peer.closed && grant != nil && voice.GrantID == grant.value.GrantID &&
		time.Now().Before(grant.deadline) && time.Now().Before(grant.value.ExpiresAt) && voice.Seq > peer.voiceSeq
	if valid {
		peer.voiceSeq = voice.Seq
	}
	peer.mu.Unlock()
	if !valid {
		return result
	}
	m.mu.Lock()
	transcriber := m.voiceTranscriber
	m.mu.Unlock()
	if transcriber == nil {
		result.Reason = "transcriber_unavailable"
		return result
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	transcript, err := transcriber.Transcribe(callCtx, voice.MimeType, audio)
	if err != nil || callCtx.Err() != nil || transcript == "" || len(transcript) > 16*1024 {
		result.Reason = "transcription_failed"
		return result
	}
	peer.mu.Lock()
	valid = !peer.closed && peer.grant == grant && time.Now().Before(grant.deadline) && time.Now().Before(grant.value.ExpiresAt)
	peer.mu.Unlock()
	if !valid {
		return result
	}
	return voiceResult{Type: protocol.MirrorVoiceTranscript, Seq: voice.Seq, Text: transcript}
}
