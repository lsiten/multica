package mirror

import (
	"errors"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	defaultMirrorSessionTTL = 30 * time.Second
	maxMirrorSessionTTL     = 5 * time.Minute
	minSessionIDBytes       = 32
)

var (
	ErrSessionNotFound         = errors.New("mirror: session not found")
	ErrSessionExpired          = errors.New("mirror: session expired")
	ErrSessionIdentityMismatch = errors.New("mirror: session identity mismatch")
	ErrSessionReplay           = errors.New("mirror: session replay")
	ErrSessionClosed           = errors.New("mirror: session closed")
	ErrInvalidSession          = errors.New("mirror: invalid session")
	ErrInvalidSessionInput     = errors.New("mirror: invalid session input")
)

type SessionState string

const (
	SessionStateOffered  SessionState = protocol.MirrorSessionStateOffered
	SessionStateAnswered SessionState = protocol.MirrorSessionStateAnswered
	SessionStateFailed   SessionState = protocol.MirrorSessionStateFailed
	SessionStateClosed   SessionState = protocol.MirrorSessionStateClosed
	SessionStateExpired  SessionState = protocol.MirrorSessionStateExpired
)

type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// SessionIdentity binds a short-lived session to every principal involved in
// the handshake. These values are metadata only; SDP is deliberately absent.
type SessionIdentity struct {
	WorkspaceID string
	RuntimeID   string
	UserID      string
	DaemonID    string
	ViewerID    string
}

// Session is the JSON-safe metadata view of a mirror session. It never carries
// SDP, frame bytes, or any other media payload.
type Session struct {
	ID            string       `json:"id"`
	WorkspaceID   string       `json:"workspace_id"`
	RuntimeID     string       `json:"runtime_id"`
	UserID        string       `json:"user_id"`
	DaemonID      string       `json:"daemon_id"`
	ViewerID      string       `json:"viewer_id"`
	CreatedAt     time.Time    `json:"created_at"`
	ExpiresAt     time.Time    `json:"expires_at"`
	State         SessionState `json:"state"`
	FailureReason string       `json:"failure_reason,omitempty"`
}

func (s Session) Metadata() SessionMetadata {
	return metadataFromSession(s)
}

// SessionMetadata is the read-only metadata shape returned after a handshake
// transition. It has no fields capable of carrying SDP or frame data.
type SessionMetadata struct {
	ID            string       `json:"id"`
	WorkspaceID   string       `json:"workspace_id"`
	RuntimeID     string       `json:"runtime_id"`
	UserID        string       `json:"user_id"`
	DaemonID      string       `json:"daemon_id"`
	ViewerID      string       `json:"viewer_id"`
	CreatedAt     time.Time    `json:"created_at"`
	ExpiresAt     time.Time    `json:"expires_at"`
	State         SessionState `json:"state"`
	FailureReason string       `json:"failure_reason,omitempty"`
	SDP           string       `json:"-"`
}

type CreateSessionInput struct {
	Identity SessionIdentity
	Offer    protocol.MirrorSessionDescription
}

type SessionStoreOptions struct {
	Clock Clock
	TTL   time.Duration
}

func normalizeIdentity(identity SessionIdentity) (SessionIdentity, error) {
	identity.WorkspaceID = strings.TrimSpace(identity.WorkspaceID)
	identity.RuntimeID = strings.TrimSpace(identity.RuntimeID)
	identity.UserID = strings.TrimSpace(identity.UserID)
	identity.DaemonID = strings.TrimSpace(identity.DaemonID)
	identity.ViewerID = strings.TrimSpace(identity.ViewerID)
	if identity.WorkspaceID == "" || identity.RuntimeID == "" || identity.UserID == "" || identity.DaemonID == "" || identity.ViewerID == "" {
		return SessionIdentity{}, ErrInvalidSessionInput
	}
	return identity, nil
}

func sameIdentity(session Session, identity SessionIdentity) bool {
	return session.WorkspaceID == identity.WorkspaceID &&
		session.RuntimeID == identity.RuntimeID &&
		session.UserID == identity.UserID &&
		session.DaemonID == identity.DaemonID &&
		session.ViewerID == identity.ViewerID
}

func newOfferedSession(id string, identity SessionIdentity, createdAt, expiresAt time.Time) Session {
	return Session{
		ID:          id,
		WorkspaceID: identity.WorkspaceID,
		RuntimeID:   identity.RuntimeID,
		UserID:      identity.UserID,
		DaemonID:    identity.DaemonID,
		ViewerID:    identity.ViewerID,
		CreatedAt:   createdAt,
		ExpiresAt:   expiresAt,
		State:       SessionStateOffered,
	}
}

func metadataFromSession(session Session) SessionMetadata {
	return SessionMetadata{
		ID:            session.ID,
		WorkspaceID:   session.WorkspaceID,
		RuntimeID:     session.RuntimeID,
		UserID:        session.UserID,
		DaemonID:      session.DaemonID,
		ViewerID:      session.ViewerID,
		CreatedAt:     session.CreatedAt,
		ExpiresAt:     session.ExpiresAt,
		State:         session.State,
		FailureReason: session.FailureReason,
	}
}
