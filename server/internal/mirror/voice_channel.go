package mirror

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

// bindVoiceChannel accepts complete short recordings from a viewer. The
// channel is capability-gated exactly like mirror-input and never writes the
// audio to logs, storage, or signaling messages.
func (m *RuntimeMirror) bindVoiceChannel(viewerID string, peer *mirrorPeer, channel *webrtc.DataChannel) {
	var mu sync.Mutex
	closed := false
	channel.OnClose(func() {
		mu.Lock()
		closed = true
		mu.Unlock()
	})
	channel.OnMessage(func(message webrtc.DataChannelMessage) {
		if !message.IsString {
			return
		}
		voice, audio, err := protocol.ParseMirrorVoiceMessage([]byte(message.Data))
		if err != nil {
			return
		}
		peer.mu.Lock()
		grant := peer.controlGrant
		valid := !peer.closed && grant != nil && voice.GrantID == grant.value.GrantID && voice.Seq > grant.voiceSeq
		if valid {
			grant.voiceSeq = voice.Seq
		}
		peer.mu.Unlock()
		if !valid {
			return
		}
		m.mu.Lock()
		transcriber := m.voiceTranscriber
		m.mu.Unlock()
		if transcriber == nil {
			return
		}
		transcript, err := transcriber.Transcribe(context.Background(), voice.MimeType, audio)
		if err != nil || transcript == "" {
			return
		}
		payload, err := json.Marshal(struct {
			Type string `json:"type"`
			Seq  uint64 `json:"seq"`
			Text string `json:"text"`
		}{protocol.MirrorVoiceTranscript, voice.Seq, transcript})
		if err != nil {
			return
		}
		mu.Lock()
		isClosed := closed
		mu.Unlock()
		if !isClosed {
			_ = channel.SendText(string(payload))
		}
	})
}
