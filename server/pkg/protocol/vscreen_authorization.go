package protocol

import (
	"fmt"
	"time"
	"unicode/utf8"
)

// MirrorAuthorizationRequest describes a consent prompt detected by the
// daemon (for example a macOS system dialog or a CLI login prompt). The daemon
// sends it over the existing P2P control channel; the API server never sees
// dialog pixels or credentials.
type MirrorAuthorizationRequest struct {
	Type      string    `json:"type"`
	RequestID string    `json:"request_id"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	ExpiresAt time.Time `json:"expires_at"`
}

const (
	MirrorAuthorizationRequestType  = "mirror-authorization:request"
	MirrorAuthorizationResponseType = "mirror-authorization:response"
)

func (r MirrorAuthorizationRequest) Validate(now time.Time) error {
	if r.Type != MirrorAuthorizationRequestType || !vscreenIdentity(r.RequestID) || r.Kind == "" || r.Title == "" || r.Message == "" || !utf8.ValidString(r.Title) || !utf8.ValidString(r.Message) || len([]rune(r.Title)) > 160 || len([]rune(r.Message)) > 2048 || !r.ExpiresAt.After(now) {
		return fmt.Errorf("%w: invalid authorization request", ErrInvalidMirrorDescription)
	}
	switch r.Kind {
	case "system", "cli":
		return nil
	default:
		return fmt.Errorf("%w: unknown authorization kind", ErrInvalidMirrorDescription)
	}
}

type MirrorAuthorizationResponse struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Approved  bool   `json:"approved"`
}

func (r MirrorAuthorizationResponse) Validate() error {
	if r.Type != MirrorAuthorizationResponseType || !vscreenIdentity(r.RequestID) {
		return fmt.Errorf("%w: invalid authorization response", ErrInvalidMirrorDescription)
	}
	return nil
}
