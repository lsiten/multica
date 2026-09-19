package mirror

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

// gestureDefaultTTL bounds a single human atomic gesture in the arbiter.
const gestureDefaultTTL = 2 * time.Second

// bindInputChannel wires the reverse "mirror-input" data channel. Messages are
// processed only while the peer holds a server-issued control grant. The
// channel being present never implies control: the grant gate fails closed.
func (m *RuntimeMirror) bindInputChannel(viewerID string, peer *mirrorPeer, channel *webrtc.DataChannel, cleanup func() error) {
	open := make(chan struct{})
	var openOnce sync.Once
	var mu sync.Mutex
	var closed bool

	channel.OnOpen(func() { openOnce.Do(func() { close(open) }) })
	channel.OnClose(func() {
		mu.Lock()
		closed = true
		mu.Unlock()
		// Losing the input channel returns the viewer to read-only but must not
		// tear down the whole viewing PeerConnection. Held gesture locks release.
		m.releaseViewerControl(viewerID, peer)
	})
	_ = cleanup

	channel.OnMessage(func(msg webrtc.DataChannelMessage) {
		m.handleInputMessage(viewerID, peer, channel, &mu, &closed, open, msg)
	})

	// Closing the control grant (revoke/expiry) must also detach the channel
	// reference so later messages are ignored until a fresh grant binds.
	peer.mu.Lock()
	if !peer.closed {
		peer.input = channel
		peer.inputClose = func() {
			// A grant ended; future messages fail the grant gate until rebind.
			// The data channel itself stays open for a fast re-grant.
		}
	}
	peer.mu.Unlock()
}

func (m *RuntimeMirror) releaseViewerControl(viewerID string, peer *mirrorPeer) {
	m.mu.Lock()
	arbiter := m.arbiter
	m.mu.Unlock()
	if arbiter != nil {
		arbiter.ReleasePrincipal(Principal{Kind: PrincipalHuman, ID: viewerID})
	}
	peer.mu.Lock()
	grant := peer.controlGrant
	peer.mu.Unlock()
	if grant != nil {
		m.RevokeControlGrant(viewerID, grant.value.GrantID, grant.generation)
	}
}

func (m *RuntimeMirror) handleInputMessage(
	viewerID string,
	peer *mirrorPeer,
	channel *webrtc.DataChannel,
	channelMu *sync.Mutex,
	channelClosed *bool,
	open <-chan struct{},
	msg webrtc.DataChannelMessage,
) {
	if msg.IsString {
		// Inputs are binary JSON frames; text is reserved for future acks only.
		return
	}
	input, err := protocol.ParseMirrorInputMessage(msg.Data)
	if err != nil {
		return
	}

	peer.mu.Lock()
	c := peer.controlGrant
	if peer.closed || c == nil || !time.Now().Before(c.deadline) || !time.Now().Before(c.value.ExpiresAt) {
		peer.mu.Unlock()
		m.nack(channel, channelMu, channelClosed, input, protocol.MirrorInputDenied)
		return
	}
	grant := c.value

	// Gate 1: grant/binding/source identity must match this exact message.
	if input.GrantID != grant.GrantID || input.NativeEpoch != grant.NativeEpoch ||
		input.DisplayGeneration != grant.SourceGeneration ||
		input.GeometryRevision == 0 || input.Seq <= c.lastSeq {
		peer.mu.Unlock()
		m.nack(channel, channelMu, channelClosed, input, protocol.MirrorInputStale)
		return
	}
	// Consume sequences before arbitration so even a rejected gesture cannot
	// be replayed later against a changed screen. Renewals retain this counter.
	c.lastSeq = input.Seq
	peer.mu.Unlock()

	m.mu.Lock()
	backend := m.controlBackend
	arbiter := m.arbiter
	m.mu.Unlock()

	// Gate 2: the host must currently permit remote human interaction.
	if backend == nil || !backend.InteractionEnabled() {
		m.nack(channel, channelMu, channelClosed, input, protocol.MirrorInputDenied)
		return
	}

	// Gate 3: resolve the granted source to the shared resource and validate epoch.
	resource, ok := backend.ResourceForGrant(grant)
	if !ok {
		m.nack(channel, channelMu, channelClosed, input, protocol.MirrorInputStale)
		return
	}
	if input.NativeEpoch != grant.NativeEpoch {
		m.nack(channel, channelMu, channelClosed, input, protocol.MirrorInputStale)
		return
	}

	if arbiter == nil {
		m.nack(channel, channelMu, channelClosed, input, protocol.MirrorInputUnsupported)
		return
	}

	// FCFS at atomic-gesture granularity per resource.
	principal := Principal{Kind: PrincipalHuman, ID: viewerID}
	switch arbiter.Acquire(resource, principal, input.GestureID, gestureDefaultTTL) {
	case AcquireBusy:
		m.nack(channel, channelMu, channelClosed, input, protocol.MirrorInputBusy)
		return
	case AcquireHeld, AcquireReentry:
	}
	// Wheel and text events are complete gestures; pointer and key gestures
	// retain the resource until their matching up event.
	releaseAtEnd := input.Kind == protocol.MirrorInputPointerUp ||
		input.Kind == protocol.MirrorInputKeyUp ||
		input.Kind == protocol.MirrorInputWheel ||
		input.Kind == protocol.MirrorInputType

	reason := backend.DispatchInput(context.Background(), resource, grant, input)
	if releaseAtEnd || reason != "" {
		arbiter.Release(resource, principal, input.GestureID)
	}
	if reason != "" {
		if reason == protocol.MirrorInputDenied {
			m.publishAuthorization(viewerID, "system", "需要主机授权", "运行时需要辅助功能权限才能接收远程输入，请在主机上确认授权。")
		}
		m.nack(channel, channelMu, channelClosed, input, reason)
		return
	}
	m.ack(channel, channelMu, channelClosed, input)
}

func (m *RuntimeMirror) ack(channel *webrtc.DataChannel, mu *sync.Mutex, closed *bool, input protocol.MirrorInputMessage) {
	m.sendAck(channel, mu, closed, protocol.MirrorInputAck{
		Type: protocol.MirrorInputAckTypeOK, Kind: string(input.Kind),
		GestureID: input.GestureID, Seq: input.Seq,
	})
}

func (m *RuntimeMirror) nack(channel *webrtc.DataChannel, mu *sync.Mutex, closed *bool, input protocol.MirrorInputMessage, reason string) {
	m.sendAck(channel, mu, closed, protocol.MirrorInputAck{
		Type: protocol.MirrorInputAckTypeNack, Kind: string(input.Kind),
		GestureID: input.GestureID, Seq: input.Seq, Reason: reason,
	})
}

func (m *RuntimeMirror) sendAck(channel *webrtc.DataChannel, mu *sync.Mutex, closed *bool, ack protocol.MirrorInputAck) {
	payload, err := json.Marshal(ack)
	if err != nil {
		return
	}
	mu.Lock()
	isClosed := *closed
	mu.Unlock()
	if isClosed {
		return
	}
	// Acks are small JSON text frames on the same channel.
	if err := channel.SendText(string(payload)); err != nil {
		_ = fmt.Sprintf("%v", err)
	}
}
