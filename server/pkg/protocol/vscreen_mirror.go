package protocol

import (
	"encoding/json"
	"fmt"
	"time"
)

// MirrorSourceKind describes a read-only viewer source, never an AI input target.
type MirrorSourceKind string

const (
	MirrorSourceVirtual  MirrorSourceKind = "virtual"
	MirrorSourcePhysical MirrorSourceKind = "physical"
	MirrorSourceSystem   MirrorSourceKind = "system"
)

// MirrorSource selects an exact display. Missing displays cannot fall back to a primary screen.
type MirrorSource struct {
	Kind     MirrorSourceKind `json:"kind"`
	SourceID string           `json:"source_id"`
}

// Validate rejects unknown source variants.
func (s MirrorSource) Validate() error {
	if !vscreenIdentity(s.SourceID) {
		return fmt.Errorf("%w: source identity is required", ErrInvalidVscreenContract)
	}
	switch s.Kind {
	case MirrorSourceVirtual, MirrorSourcePhysical, MirrorSourceSystem:
		return nil
	default:
		return fmt.Errorf("%w: unknown source kind", ErrInvalidVscreenContract)
	}
}

// MirrorSourceBinding is supplied by the authenticated native source registry.
// Physical/system entries are authorization projections for this resource, not display owners.
type MirrorSourceBinding struct {
	Resource    ResourceKey  `json:"resource"`
	Source      MirrorSource `json:"source"`
	NativeEpoch string       `json:"native_epoch"`
	Generation  string       `json:"generation"`
	Primary     bool         `json:"primary"`
}

// MirrorChatTaskContext is the authenticated source contract carried with a
// mirror-directed chat task. It is deliberately separate from message text so
// the daemon can reject a stale or mismatched display without parsing a
// user-authored string.
type MirrorChatTaskContext struct {
	Type   string              `json:"type"`
	Source MirrorSourceBinding `json:"source"`
}

const MirrorChatTaskContextType = "mirror_source_v1"

func (c MirrorChatTaskContext) Validate() error {
	if c.Type != MirrorChatTaskContextType {
		return fmt.Errorf("%w: unknown mirror chat context", ErrInvalidVscreenContract)
	}
	if err := c.Source.Source.Validate(); err != nil {
		return err
	}
	if err := c.Source.Resource.Validate(); err != nil {
		return err
	}
	if !vscreenIdentity(c.Source.NativeEpoch) || !vscreenIdentity(c.Source.Generation) {
		return fmt.Errorf("%w: incomplete mirror source binding", ErrInvalidVscreenContract)
	}
	return nil
}

// MirrorViewerGrant binds renewable viewing permission independently of negotiation expiry.
// It is server-issued metadata, not a client-provided proof or an input capability.
type MirrorViewerGrant struct {
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

func (p MirrorOfferPayload) validateSourceContract() error {
	if p.ViewerGrant != nil {
		g := p.ViewerGrant
		if err := g.Source.Validate(); err != nil {
			return err
		}
		if !vscreenIdentity(g.GrantID) || !vscreenIdentity(g.NativeEpoch) || !vscreenIdentity(g.SourceGeneration) || g.ExpiresAt.IsZero() || g.SessionID != p.SessionID || g.WorkspaceID != p.WorkspaceID || g.RuntimeID != p.RuntimeID || g.UserID != p.UserID || g.ViewerID != p.ViewerID {
			return fmt.Errorf("%w: viewer grant binding mismatch", ErrInvalidMirrorDescription)
		}
	}
	switch p.ProtocolVersion {
	case 0, 1:
		if p.Source != nil || p.Transport != "" || p.SourceGeneration != "" || p.NativeEpoch != "" {
			return fmt.Errorf("%w: source selection requires mirror v2", ErrInvalidMirrorDescription)
		}
		if p.ViewerGrant != nil && p.ViewerGrant.Source.Kind == MirrorSourceVirtual {
			return fmt.Errorf("%w: legacy grant requires physical or system source", ErrInvalidMirrorDescription)
		}
		return nil
	case 2:
		if p.Transport != "video" || p.Source == nil || !vscreenIdentity(p.SourceGeneration) || !vscreenIdentity(p.NativeEpoch) || p.ViewerGrant == nil {
			return fmt.Errorf("%w: incomplete mirror v2 source contract", ErrInvalidMirrorDescription)
		}
		if err := p.Source.Validate(); err != nil {
			return fmt.Errorf("mirror source: %w", err)
		}
		g := p.ViewerGrant
		if g.NativeEpoch != p.NativeEpoch || g.Source != *p.Source || g.SourceGeneration != p.SourceGeneration {
			return fmt.Errorf("%w: viewer grant binding mismatch", ErrInvalidMirrorDescription)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported mirror protocol version", ErrInvalidMirrorDescription)
	}
}

// MirrorAuthorization must come from the authenticated connection and current native registry.
// ParseMirrorOffer checks consistency against this authority; it does not authenticate a token.
type MirrorAuthorization struct {
	Resource           ResourceKey
	DaemonID           string
	UserID             string
	NativeEpoch        string
	Now                time.Time
	Sources            []MirrorSourceBinding
	RequireViewerGrant bool
}

// ParseMirrorOffer decodes a bounded payload and rejects stale or foreign sources without fallback.
func ParseMirrorOffer(raw []byte, auth MirrorAuthorization) (MirrorOfferPayload, error) {
	var p MirrorOfferPayload
	if len(raw) > MaxMirrorSDPBytes+16*1024 {
		return p, ErrMirrorSDPTooLarge
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("decode mirror offer: %w", err)
	}
	if err := p.Validate(); err != nil {
		return MirrorOfferPayload{}, err
	}
	if err := auth.Resource.Validate(); err != nil {
		return MirrorOfferPayload{}, err
	}
	if auth.Now.IsZero() || !p.ExpiresAt.After(auth.Now) || p.WorkspaceID != auth.Resource.WorkspaceID || p.RuntimeID != auth.Resource.RuntimeID || p.UserID != auth.UserID || p.DaemonID != auth.DaemonID {
		return MirrorOfferPayload{}, fmt.Errorf("%w: expired or unauthorized mirror offer", ErrInvalidMirrorDescription)
	}
	if p.ViewerGrant == nil && auth.RequireViewerGrant {
		return MirrorOfferPayload{}, fmt.Errorf("%w: viewer grant is required", ErrInvalidMirrorDescription)
	}
	if p.ViewerGrant == nil {
		return p, nil
	}
	g := p.ViewerGrant
	if g.NativeEpoch != auth.NativeEpoch || !g.ExpiresAt.After(auth.Now) {
		return MirrorOfferPayload{}, fmt.Errorf("%w: stale native epoch or viewer grant", ErrInvalidMirrorDescription)
	}
	for _, binding := range auth.Sources {
		if binding.Resource == auth.Resource && binding.Source == g.Source && binding.NativeEpoch == g.NativeEpoch && binding.Generation == g.SourceGeneration {
			if p.ProtocolVersion != 2 && !binding.Primary {
				return MirrorOfferPayload{}, fmt.Errorf("%w: legacy viewer requires current primary source", ErrInvalidMirrorDescription)
			}
			return p, nil
		}
	}
	return MirrorOfferPayload{}, fmt.Errorf("%w: source is unavailable or unauthorized", ErrInvalidMirrorDescription)
}

// MirrorViewerRevokePayload closes a single grant rather than changing any AI lease.
type MirrorViewerRevokePayload struct {
	WorkspaceID      string `json:"workspace_id"`
	RuntimeID        string `json:"runtime_id"`
	DaemonGeneration string `json:"daemon_generation"`
	SessionID        string `json:"session_id"`
	ViewerID         string `json:"viewer_id"`
	GrantID          string `json:"grant_id"`
}
