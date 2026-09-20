package mirror

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const defaultMirrorPeerAttachTimeout = 30 * time.Second

const relayCandidateGrace = 1500 * time.Millisecond

var (
	ErrInvalidOffer    = errors.New("mirror: invalid offer")
	ErrDuplicateViewer = errors.New("mirror: viewer is already connected")
	ErrMirrorClosed    = errors.New("mirror: runtime mirror is closed")
	ErrViewerClosed    = errors.New("mirror: viewer data channel is closed")
)

// ICEConfig contains the ICE servers used by daemon-side PeerConnections.
// Servers and ICEServers are accepted as equivalent spellings so callers can
// use either the concise configuration form or the Pion-shaped form.
type ICEConfig struct {
	Servers        []string           `json:"servers,omitempty"`
	ICEServers     []webrtc.ICEServer `json:"iceServers,omitempty"`
	STUNServers    []string           `json:"stunServers,omitempty"`
	TURNServers    []string           `json:"turnServers,omitempty"`
	TURNUsername   string             `json:"turnUsername,omitempty"`
	TURNCredential string             `json:"turnCredential,omitempty"`
}

func (c ICEConfig) Pion() webrtc.Configuration {
	iceServers := append([]webrtc.ICEServer(nil), c.ICEServers...)
	if len(c.Servers) > 0 {
		iceServers = append(iceServers, webrtc.ICEServer{URLs: append([]string(nil), c.Servers...)})
	}
	if len(c.STUNServers) > 0 {
		iceServers = append(iceServers, webrtc.ICEServer{URLs: append([]string(nil), c.STUNServers...)})
	}
	if len(c.TURNServers) > 0 {
		iceServers = append(iceServers, webrtc.ICEServer{
			URLs:       append([]string(nil), c.TURNServers...),
			Username:   c.TURNUsername,
			Credential: c.TURNCredential,
		})
	}
	return webrtc.Configuration{ICEServers: iceServers}
}

// SessionDescription is the JSON-safe SDP representation used by the daemon
// control plane. Screen data never passes through this representation.
type SessionDescription struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

func SessionDescriptionFromPion(description webrtc.SessionDescription) SessionDescription {
	return SessionDescription{Type: description.Type.String(), SDP: description.SDP}
}

func (description SessionDescription) Pion() webrtc.SessionDescription {
	return webrtc.SessionDescription{Type: webrtc.NewSDPType(strings.ToLower(strings.TrimSpace(description.Type))), SDP: description.SDP}
}

type RuntimeMirror struct {
	source *Source
	hub    *CaptureHub

	mu                         sync.Mutex
	closed                     bool
	peers                      map[string]*mirrorPeer
	closeDone                  chan struct{}
	closeErr                   error
	peerAttachTimeout          time.Duration
	arbiter                    *Arbiter
	controlBackend             ControlBackend
	voiceTranscriber           VoiceTranscriber
	authorizationHandler       func(context.Context, protocol.MirrorAuthorizationRequest, bool) error
	authorizationPublisher     func(string, protocol.MirrorAuthorizationRequest)
	authorizationMu            sync.Mutex
	pendingAuthorizations      map[string]pendingAuthorization
	controlMu                  sync.Mutex
	controlStateHookGeneration uint64
	controlStateHookFn         func(ControlStateChange)
}

// VoiceTranscriber runs on the daemon host. It must not persist audio or
// transcripts; callers may forward the returned text to an existing agent
// conversation without creating a new task.
type VoiceTranscriber interface {
	Transcribe(ctx context.Context, mimeType string, audio []byte) (string, error)
}

func (m *RuntimeMirror) SetVoiceTranscriber(transcriber VoiceTranscriber) {
	m.mu.Lock()
	m.voiceTranscriber = transcriber
	m.mu.Unlock()
}

// SetAuthorizationHandler receives explicit viewer consent decisions. The
// daemon owns the pending request and decides how to apply the result.
func (m *RuntimeMirror) SetAuthorizationHandler(handler func(context.Context, protocol.MirrorAuthorizationRequest, bool) error) {
	m.mu.Lock()
	m.authorizationHandler = handler
	m.mu.Unlock()
}

func (m *RuntimeMirror) SetAuthorizationPublisher(publisher func(string, protocol.MirrorAuthorizationRequest)) {
	m.mu.Lock()
	m.authorizationPublisher = publisher
	m.mu.Unlock()
}

func (m *RuntimeMirror) publishAuthorization(viewerID, kind, title, message string) {
	m.mu.Lock()
	publisher := m.authorizationPublisher
	m.mu.Unlock()
	if publisher == nil {
		return
	}
	publisher(viewerID, protocol.MirrorAuthorizationRequest{
		Type: protocol.MirrorAuthorizationRequestType, RequestID: uuid.NewString(), Kind: kind,
		Title: title, Message: message, ExpiresAt: time.Now().Add(2 * time.Minute),
	})
}

// ControlBackend is supplied by the daemon. It resolves a granted source to a
// shared arbitration resource, reports whether the host currently permits
// remote human interaction (default off), and applies one accepted input.
// DispatchInput returns "" when applied or a MirrorInput* nack reason.
type ControlBackend interface {
	ResourceForGrant(grant protocol.MirrorControlGrant) (protocol.ResourceKey, bool)
	InteractionEnabled() bool
	DispatchInput(ctx context.Context, resource protocol.ResourceKey, grant protocol.MirrorControlGrant, msg protocol.MirrorInputMessage) string
}

// SetArbiter installs the daemon-shared FCFS arbiter so human input and agent
// actions contend on the same per-resource locks.
func (m *RuntimeMirror) SetArbiter(a *Arbiter) {
	m.mu.Lock()
	m.arbiter = a
	m.mu.Unlock()
}

func (m *RuntimeMirror) Arbiter() *Arbiter {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.arbiter
}

// SetControlBackend installs the daemon-side host gate and input executor.
func (m *RuntimeMirror) SetControlBackend(backend ControlBackend) {
	m.mu.Lock()
	m.controlBackend = backend
	m.mu.Unlock()
}

type mirrorPeer struct {
	pc           *webrtc.PeerConnection
	video        *videoPeer
	grant        *viewerGrant
	controlGrant *controlGrant
	input        *webrtc.DataChannel
	inputClose   func()
	inputLocks   map[string]string

	mu                     sync.Mutex
	attachTimer            *time.Timer
	stopNegotiationCleanup func() bool
	negotiationCommitted   bool
	closed                 bool
	detach                 func()
	done                   chan struct{}
	closeErr               error
	attachOnce             sync.Once
	closeOnce              sync.Once
}

// NewRuntimeMirror creates one shared capture source for a runtime.
func NewRuntimeMirror(capturer Capturer, interval time.Duration) *RuntimeMirror {
	return newRuntimeMirror(capturer, interval, defaultMirrorPeerAttachTimeout)
}

func newRuntimeMirror(capturer Capturer, interval, peerAttachTimeout time.Duration) *RuntimeMirror {
	if peerAttachTimeout <= 0 {
		peerAttachTimeout = defaultMirrorPeerAttachTimeout
	}
	return &RuntimeMirror{
		source:                NewSource(capturer, interval),
		peers:                 make(map[string]*mirrorPeer),
		pendingAuthorizations: make(map[string]pendingAuthorization),
		peerAttachTimeout:     peerAttachTimeout,
	}
}

// SetCaptureFailureHandler observes terminal shared-source capture failures.
func (m *RuntimeMirror) SetCaptureFailureHandler(handler func(error)) {
	m.source.SetCaptureFailureHandler(handler)
}

// SetViewerStateHook observes shared source viewer transitions and replays
// active viewers needed after a control connection reconnects.
func (m *RuntimeMirror) SetViewerStateHook(hook func(ViewerStateChange), generation uint64) bool {
	return m.source.SetViewerStateHook(hook, generation)
}

// HasViewers reports whether at least one negotiated DataChannel is attached.
func (m *RuntimeMirror) HasViewers() bool {
	return m.source.HasViewers()
}

// Answer negotiates one browser offer and returns the daemon answer. Every
// viewer owns an independent PeerConnection, while all viewers share source.
func (m *RuntimeMirror) Answer(ctx context.Context, viewerID string, offer SessionDescription, ice ICEConfig) (Negotiation, error) {
	return m.answer(ctx, viewerID, offer, ice, nil, "")
}

func (m *RuntimeMirror) answer(ctx context.Context, viewerID string, offer SessionDescription, ice ICEConfig, setup func(*mirrorPeer) error, videoFmtp string) (Negotiation, error) {
	viewerID = strings.TrimSpace(viewerID)
	if viewerID == "" {
		return Negotiation{}, fmt.Errorf("%w: viewer id is required", ErrInvalidOffer)
	}
	if offer.Pion().Type != webrtc.SDPTypeOffer || strings.TrimSpace(offer.SDP) == "" {
		return Negotiation{}, fmt.Errorf("%w: type must be offer and sdp must be non-empty", ErrInvalidOffer)
	}
	if err := ctx.Err(); err != nil {
		return Negotiation{}, fmt.Errorf("mirror: answer before negotiation: %w", err)
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Negotiation{}, ErrMirrorClosed
	}
	if _, exists := m.peers[viewerID]; exists {
		m.mu.Unlock()
		return Negotiation{}, ErrDuplicateViewer
	}
	var pc *webrtc.PeerConnection
	var err error
	if setup != nil {
		pc, err = newVideoPeerConnection(ice, videoFmtp)
	} else {
		pc, err = webrtc.NewPeerConnection(ice.Pion())
	}
	if err != nil {
		m.mu.Unlock()
		return Negotiation{}, fmt.Errorf("mirror: create peer connection: %w", err)
	}
	peer := &mirrorPeer{pc: pc, done: make(chan struct{})}
	m.peers[viewerID] = peer
	m.mu.Unlock()

	cleanup := func() error { return m.removePeer(viewerID, peer, true) }
	stopCancelCleanup := context.AfterFunc(ctx, func() {
		peer.mu.Lock()
		closed := peer.closed
		attached := peer.detach != nil
		committed := peer.negotiationCommitted
		peer.mu.Unlock()
		if !closed && !attached && !committed {
			_, _ = m.removeUnattachedPeer(viewerID, peer, true, false)
		}
	})
	peer.mu.Lock()
	peer.stopNegotiationCleanup = stopCancelCleanup
	peer.mu.Unlock()
	if setup != nil {
		if err := setup(peer); err != nil {
			_ = cleanup()
			return Negotiation{}, err
		}
	}
	pc.OnDataChannel(func(channel *webrtc.DataChannel) {
		if channel.Label() == "mirror-input" && channel.Ordered() {
			m.bindInputChannel(viewerID, peer, channel, cleanup)
			return
		}
		if channel.Label() == "mirror-voice" && channel.Ordered() {
			m.bindVoiceChannel(viewerID, peer, channel)
			return
		}
		if peer.video != nil {
			if (channel.Label() == "mirror" || channel.Label() == "mirror-control") && channel.Ordered() {
				peer.mu.Lock()
				if !peer.closed {
					peer.video.control = channel
				}
				peer.mu.Unlock()
				channel.OnOpen(func() {
					if err := m.sendVideoMetadata(peer, 0, true); err != nil {
						_ = cleanup()
					}
				})
				channel.OnMessage(func(message webrtc.DataChannelMessage) {
					m.handleAuthorizationMessage(peer, message)
				})
				channel.OnClose(func() { _ = cleanup() })
				return
			}
			_ = channel.Close()
			return
		}
		if channel.Label() != "mirror" || !channel.Ordered() {
			_ = channel.Close()
			return
		}
		sink := dataChannelSink{channel: channel}
		channel.OnOpen(func() {
			peer.attachOnce.Do(func() {
				peer.mu.Lock()
				if peer.closed {
					peer.mu.Unlock()
					return
				}
				peer.detach = m.source.AddViewer(viewerID, sink)
				if peer.attachTimer != nil {
					peer.attachTimer.Stop()
				}
				stopNegotiationCleanup := peer.stopNegotiationCleanup
				peer.stopNegotiationCleanup = nil
				peer.mu.Unlock()
				if stopNegotiationCleanup != nil {
					stopNegotiationCleanup()
				}
			})
		})
		channel.OnClose(func() { _ = cleanup() })
	})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected && peer.video != nil {
			peer.attachOnce.Do(func() { m.attachVideo(viewerID, peer) })
		}
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			_ = cleanup()
		}
	})

	if err := pc.SetRemoteDescription(offer.Pion()); err != nil {
		_ = cleanup()
		return Negotiation{}, fmt.Errorf("%w: set remote description: %v", ErrInvalidOffer, err)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = cleanup()
		return Negotiation{}, fmt.Errorf("mirror: create answer: %w", err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	// Resolve shortly after the first relay candidate: on networks that block
	// UDP TURN, gathering "complete" only arrives after the blocked transports
	// exhaust retransmissions (>10s), while the usable TCP/TLS relay candidate
	// arrives within ~1s. Waiting for completion could exceed the session TTL.
	relaySeen := make(chan struct{})
	var relayOnce sync.Once
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		if c.Typ == webrtc.ICECandidateTypeRelay {
			relayOnce.Do(func() { close(relaySeen) })
		}
	})
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = cleanup()
		return Negotiation{}, fmt.Errorf("mirror: set local description: %w", err)
	}
	gatherCtx, stopGather := context.WithCancel(ctx)
	defer stopGather()
	select {
	case <-gathered:
	case <-waitRelayGrace(gatherCtx, relaySeen):
	case <-ctx.Done():
		_ = cleanup()
		return Negotiation{}, fmt.Errorf("mirror: gather answer: %w", ctx.Err())
	}
	local := pc.LocalDescription()
	if local == nil || local.SDP == "" {
		_ = cleanup()
		return Negotiation{}, errors.New("mirror: local answer is empty")
	}
	if err := ctx.Err(); err != nil {
		_ = cleanup()
		return Negotiation{}, fmt.Errorf("mirror: answer after negotiation: %w", err)
	}
	peer.mu.Lock()
	if !peer.closed && peer.detach == nil {
		peer.attachTimer = time.AfterFunc(m.peerAttachTimeout, func() {
			_ = m.removePeer(viewerID, peer, true)
		})
	}
	peer.mu.Unlock()
	return Negotiation{
		SessionDescription: SessionDescriptionFromPion(*local),
		runtimeMirror:      m,
		viewerID:           viewerID,
		peer:               peer,
	}, nil
}

// Negotiation is a successfully created daemon answer that has not yet been
// proven deliverable to its control WebSocket. Abandon closes only this exact
// peer, never a replacement that reused the same viewer ID after reconnect.
type Negotiation struct {
	VideoQuality *VideoQuality `json:"video_quality,omitempty"`
	SessionDescription
	runtimeMirror *RuntimeMirror
	viewerID      string
	peer          *mirrorPeer
}

// Commit transfers an answered peer from control-connection cleanup to the
// normal DataChannel attach timeout.
func (n Negotiation) Commit() {
	if n.peer == nil {
		return
	}
	n.peer.mu.Lock()
	stop := n.peer.stopNegotiationCleanup
	n.peer.stopNegotiationCleanup = nil
	n.peer.negotiationCommitted = true
	n.peer.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// Abandon closes an answer that became stale before the daemon sent it.
func (n Negotiation) Abandon() error {
	if n.runtimeMirror == nil || n.peer == nil {
		return nil
	}
	return n.runtimeMirror.removePeer(n.viewerID, n.peer, false)
}

type peerRef struct {
	viewerID string
	peer     *mirrorPeer
}

// waitRelayGrace returns a channel that closes after a short grace window
// following the first relay candidate, giving cheap host/srflx candidates a
// chance to arrive without waiting for blocked TURN transports to give up.
func waitRelayGrace(ctx context.Context, relaySeen <-chan struct{}) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-relaySeen:
		case <-ctx.Done():
			return
		}
		timer := time.NewTimer(relayCandidateGrace)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}()
	return done
}
