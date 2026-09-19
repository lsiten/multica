package protocol

import (
	"errors"
	"fmt"
	"time"
)

// DaemonCapabilityScreenControlV1 advertises a daemon that accepts human
// pointer/keyboard input on a mirror session through the mirror-input
// data channel. It is independent of the read-only viewer grant capability.
const DaemonCapabilityScreenControlV1 = "screen-control-v1"

// Control grant control-plane events. They travel only over the authenticated
// daemon control WebSocket and never carry input payloads.
const (
	// EventMirrorControlGrant binds or renews one viewer's input capability.
	EventMirrorControlGrant = "mirror:control-grant"
	// EventMirrorControlRevoke closes one viewer's input capability immediately.
	EventMirrorControlRevoke = "mirror:control-revoke"
	// EventMirrorControlState reports daemon-observed control binding transitions.
	// It contains identity/source metadata only, never pointer, key, text, or coordinates.
	EventMirrorControlState = "mirror:control-state"
)

// Inbox item types for the runtime owner.
const (
	InboxTypeRuntimeControlStarted = "runtime_mirror_control_started"
	InboxTypeRuntimeControlStopped = "runtime_mirror_control_stopped"
)

// ErrInvalidControlGrant identifies a malformed or inconsistent control grant.
var ErrInvalidControlGrant = errors.New("protocol: invalid mirror control grant")

// MirrorControlGrant is a server-issued, short-lived capability for a human to
// inject input into one exact mirror source. Unlike MirrorViewerGrant it is an
// explicit input capability; possession of a viewer grant never implies one.
type MirrorControlGrant struct {
	GrantID          string       `json:"grant_id"`
	SessionID        string       `json:"session_id"`
	WorkspaceID      string       `json:"workspace_id"`
	RuntimeID        string       `json:"runtime_id"`
	UserID           string       `json:"user_id"`
	ViewerID         string       `json:"viewer_id"`
	NativeEpoch      string       `json:"native_epoch"`
	Source           MirrorSource `json:"source"`
	SourceGeneration string       `json:"source_generation"`
	ExpiresAt        time.Time    `json:"expires_at"`
}

// Validate enforces a complete, bound-to-one-source grant with a future expiry.
func (g MirrorControlGrant) Validate(now time.Time) error {
	if !vscreenIdentity(g.GrantID) || !vscreenIdentity(g.SessionID) || !vscreenIdentity(g.WorkspaceID) ||
		!vscreenIdentity(g.RuntimeID) || !vscreenIdentity(g.UserID) || !vscreenIdentity(g.ViewerID) ||
		!vscreenIdentity(g.NativeEpoch) || !vscreenIdentity(g.SourceGeneration) {
		return fmt.Errorf("%w: identity is incomplete", ErrInvalidControlGrant)
	}
	if err := g.Source.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidControlGrant, err)
	}
	if g.ExpiresAt.IsZero() || !g.ExpiresAt.After(now) {
		return fmt.Errorf("%w: grant must not be expired", ErrInvalidControlGrant)
	}
	return nil
}

// EqualIdentity reports whether two grants describe the same bound capability.
func (g MirrorControlGrant) EqualIdentity(other MirrorControlGrant) bool {
	return g.GrantID == other.GrantID && g.SessionID == other.SessionID &&
		g.WorkspaceID == other.WorkspaceID && g.RuntimeID == other.RuntimeID &&
		g.UserID == other.UserID && g.ViewerID == other.ViewerID &&
		g.NativeEpoch == other.NativeEpoch && g.Source == other.Source &&
		g.SourceGeneration == other.SourceGeneration
}

// MirrorControlGrantPayload delivers a bind/renew to the owning daemon. The
// generation is checked against the authenticated control connection.
type MirrorControlGrantPayload struct {
	WorkspaceID      string             `json:"workspace_id"`
	RuntimeID        string             `json:"runtime_id"`
	DaemonGeneration string             `json:"daemon_generation"`
	Grant            MirrorControlGrant `json:"grant"`
}

// MirrorControlRevokePayload immediately removes one viewer's input capability.
type MirrorControlRevokePayload struct {
	WorkspaceID      string `json:"workspace_id"`
	RuntimeID        string `json:"runtime_id"`
	DaemonGeneration string `json:"daemon_generation"`
	ViewerID         string `json:"viewer_id"`
	GrantID          string `json:"grant_id"`
}

// MirrorControlStatePayload reports one viewer's human-control capability becoming
// active or inactive. It is metadata only and must never carry input payloads.
type MirrorControlStatePayload struct {
	WorkspaceID string       `json:"workspace_id"`
	RuntimeID   string       `json:"runtime_id"`
	DaemonID    string       `json:"daemon_id"`
	ViewerID    string       `json:"viewer_id"`
	UserID      string       `json:"user_id"`
	Source      MirrorSource `json:"source"`
	Active      bool         `json:"active"`
}

// Validate enforces a fully scoped controller identity and a valid source kind.
func (p MirrorControlStatePayload) Validate() error {
	if !vscreenIdentity(p.WorkspaceID) || !vscreenIdentity(p.RuntimeID) ||
		!vscreenIdentity(p.DaemonID) || !vscreenIdentity(p.ViewerID) || !vscreenIdentity(p.UserID) {
		return fmt.Errorf("%w: control state identity is incomplete", ErrInvalidControlGrant)
	}
	if err := p.Source.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidControlGrant, err)
	}
	return nil
}

// RuntimeMirrorControlController is one active human controller broadcast to
// workspace members. It contains metadata only and never includes input data.
type RuntimeMirrorControlController struct {
	ViewerID string       `json:"viewer_id"`
	UserID   string       `json:"user_id"`
	Source   MirrorSource `json:"source"`
}

// RuntimeMirrorControlPayload announces the current active controller set for
// one runtime to all workspace viewers.
type RuntimeMirrorControlPayload struct {
	WorkspaceID string                           `json:"workspace_id"`
	RuntimeID   string                           `json:"runtime_id"`
	Controllers []RuntimeMirrorControlController `json:"controllers"`
}
