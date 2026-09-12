package protocol

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	EventMirrorOffer         = "mirror:offer"
	EventMirrorAnswer        = "mirror:answer"
	EventMirrorAnswerFailure = "mirror:answer-failed"
	EventMirrorViewer        = "mirror:viewer"

	InboxTypeRuntimeMirrorViewerStarted = "runtime_mirror_viewer_started"
	InboxTypeRuntimeMirrorViewerStopped = "runtime_mirror_viewer_stopped"

	MirrorSessionStateOffered  = "offered"
	MirrorSessionStateAnswered = "answered"
	MirrorSessionStateFailed   = "failed"
	MirrorSessionStateClosed   = "closed"
	MirrorSessionStateExpired  = "expired"

	MaxMirrorSDPBytes = 256 * 1024
)

var (
	ErrInvalidMirrorDescription = errors.New("protocol: invalid mirror session description")
	ErrMirrorSDPTooLarge        = errors.New("protocol: mirror sdp is too large")
)

const (
	MirrorAnswerFailurePermissionDenied   = "permission-denied"
	MirrorAnswerFailureUnsupported        = "unsupported"
	MirrorAnswerFailureNoDisplay          = "no-display"
	MirrorAnswerFailureCaptureUnavailable = "capture-unavailable"
	MirrorAnswerFailureNegotiation        = "negotiation-failed"
)

// MirrorSessionDescription is the only protocol type that carries WebRTC SDP.
// It is bounded at the protocol boundary and must never be logged or persisted
// as session metadata.
type MirrorSessionDescription struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

// MirrorICEServer is the browser-compatible STUN/TURN configuration shared
// with both negotiation peers.
type MirrorICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// MirrorICEConfig carries the deployment-provided STUN/TURN configuration.
// TURNConfigured lets the UI warn that restrictive NATs may block the direct
// peer connection when no relay is configured.
type MirrorICEConfig struct {
	ICEServers     []MirrorICEServer `json:"ice_servers"`
	TURNConfigured bool              `json:"turn_configured"`
}

// Validate checks the wire-level invariants required by the mirror handshake.
func (d MirrorSessionDescription) Validate(expectedType string) error {
	if strings.ToLower(strings.TrimSpace(d.Type)) != expectedType || strings.TrimSpace(d.SDP) == "" {
		return fmt.Errorf("%w: type must be %q and sdp must be non-empty", ErrInvalidMirrorDescription, expectedType)
	}
	if len([]byte(d.SDP)) > MaxMirrorSDPBytes {
		return ErrMirrorSDPTooLarge
	}
	return nil
}

// MirrorOfferPayload is sent to a daemon to start one viewer-bound handshake.
type MirrorOfferPayload struct {
	SessionID   string                   `json:"session_id"`
	WorkspaceID string                   `json:"workspace_id"`
	RuntimeID   string                   `json:"runtime_id"`
	UserID      string                   `json:"user_id"`
	DaemonID    string                   `json:"daemon_id"`
	ViewerID    string                   `json:"viewer_id"`
	Offer       MirrorSessionDescription `json:"offer"`
	ICEConfig   MirrorICEConfig          `json:"ice_config"`
	ExpiresAt   time.Time                `json:"expires_at"`
}

// MirrorAnswerPayload is sent from a daemon after consuming a single offer.
type MirrorAnswerPayload struct {
	SessionID   string                   `json:"session_id"`
	WorkspaceID string                   `json:"workspace_id"`
	RuntimeID   string                   `json:"runtime_id"`
	UserID      string                   `json:"user_id"`
	DaemonID    string                   `json:"daemon_id"`
	ViewerID    string                   `json:"viewer_id"`
	Answer      MirrorSessionDescription `json:"answer"`
	ExpiresAt   time.Time                `json:"expires_at"`
}

// MirrorAnswerFailurePayload reports that a daemon could not answer one
// offer. It carries only the fixed reason taxonomy; internal errors and SDP
// never leave the daemon.
type MirrorAnswerFailurePayload struct {
	SessionID   string    `json:"session_id"`
	WorkspaceID string    `json:"workspace_id"`
	RuntimeID   string    `json:"runtime_id"`
	UserID      string    `json:"user_id"`
	DaemonID    string    `json:"daemon_id"`
	ViewerID    string    `json:"viewer_id"`
	Reason      string    `json:"reason"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// MirrorViewerPayload reports shared-source viewer 0-to-1 and 1-to-0
// transitions. It carries no screen data.
type MirrorViewerPayload struct {
	WorkspaceID string `json:"workspace_id"`
	RuntimeID   string `json:"runtime_id"`
	DaemonID    string `json:"daemon_id"`
	ViewerID    string `json:"viewer_id,omitempty"`
	Active      bool   `json:"active"`
}

func (p MirrorOfferPayload) Validate() error {
	if strings.TrimSpace(p.SessionID) == "" || strings.TrimSpace(p.WorkspaceID) == "" || strings.TrimSpace(p.RuntimeID) == "" || strings.TrimSpace(p.UserID) == "" || strings.TrimSpace(p.DaemonID) == "" || strings.TrimSpace(p.ViewerID) == "" {
		return fmt.Errorf("%w: mirror offer identity is incomplete", ErrInvalidMirrorDescription)
	}
	if p.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: mirror offer expiry is required", ErrInvalidMirrorDescription)
	}
	if err := p.Offer.Validate("offer"); err != nil {
		return fmt.Errorf("mirror offer payload: %w", err)
	}
	return nil
}

func (p MirrorAnswerPayload) Validate() error {
	if strings.TrimSpace(p.SessionID) == "" || strings.TrimSpace(p.WorkspaceID) == "" || strings.TrimSpace(p.RuntimeID) == "" || strings.TrimSpace(p.UserID) == "" || strings.TrimSpace(p.DaemonID) == "" || strings.TrimSpace(p.ViewerID) == "" {
		return fmt.Errorf("%w: mirror answer identity is incomplete", ErrInvalidMirrorDescription)
	}
	if p.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: mirror answer expiry is required", ErrInvalidMirrorDescription)
	}
	if err := p.Answer.Validate("answer"); err != nil {
		return fmt.Errorf("mirror answer payload: %w", err)
	}
	return nil
}

func (p MirrorAnswerFailurePayload) Validate() error {
	if strings.TrimSpace(p.SessionID) == "" || strings.TrimSpace(p.WorkspaceID) == "" || strings.TrimSpace(p.RuntimeID) == "" || strings.TrimSpace(p.UserID) == "" || strings.TrimSpace(p.DaemonID) == "" || strings.TrimSpace(p.ViewerID) == "" {
		return fmt.Errorf("%w: mirror answer failure identity is incomplete", ErrInvalidMirrorDescription)
	}
	if p.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: mirror answer failure expiry is required", ErrInvalidMirrorDescription)
	}
	if !validMirrorAnswerFailureReason(p.Reason) {
		return fmt.Errorf("%w: unsupported mirror answer failure reason", ErrInvalidMirrorDescription)
	}
	return nil
}

func validMirrorAnswerFailureReason(reason string) bool {
	switch reason {
	case MirrorAnswerFailurePermissionDenied,
		MirrorAnswerFailureUnsupported,
		MirrorAnswerFailureNoDisplay,
		MirrorAnswerFailureCaptureUnavailable,
		MirrorAnswerFailureNegotiation:
		return true
	default:
		return false
	}
}

func (p MirrorViewerPayload) Validate() error {
	if strings.TrimSpace(p.WorkspaceID) == "" || strings.TrimSpace(p.RuntimeID) == "" || strings.TrimSpace(p.DaemonID) == "" {
		return fmt.Errorf("%w: mirror viewer identity is incomplete", ErrInvalidMirrorDescription)
	}
	return nil
}
