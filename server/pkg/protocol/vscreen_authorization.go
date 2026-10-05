package protocol

import (
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"
)

// MirrorAuthorizationRequest describes a consent prompt detected by the
// daemon (for example a macOS system dialog or a CLI login prompt). The daemon
// sends it over the existing P2P control channel; the API server never sees
// dialog pixels or credentials.
type MirrorAuthorizationRequest struct {
	Operation *MirrorCLIOperation `json:"operation,omitempty"`
	Type      string              `json:"type"`
	RequestID string              `json:"request_id"`
	Kind      string              `json:"kind"`
	Title     string              `json:"title"`
	Message   string              `json:"message"`
	ExpiresAt time.Time           `json:"expires_at"`
}

// MirrorCLIOperation keeps exact operation detail on the local review channel.
type MirrorCLIOperation struct {
	Permissions *MirrorCLIPermissions `json:"permissions,omitempty"`
	Files       []MirrorCLIFileChange `json:"files,omitempty"`
	Kind        string                `json:"kind"`
	Target      string                `json:"target"`
	Location    string                `json:"location,omitempty"`
	Reason      string                `json:"reason,omitempty"`
	Details     string                `json:"details"`
}

// MirrorCLIPermissions is the recognized permission scope for the current turn.
type MirrorCLIPermissions struct {
	NetworkEnabled *bool    `json:"network_enabled,omitempty"`
	ReadPaths      []string `json:"read_paths,omitempty"`
	WritePaths     []string `json:"write_paths,omitempty"`
}

// MirrorCLIFileChange is the provider's exact file scope on the local channel.
type MirrorCLIFileChange struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	MovePath string `json:"move_path,omitempty"`
}

const (
	MirrorAuthorizationRequestType  = "mirror-authorization:request"
	MirrorAuthorizationResponseType = "mirror-authorization:response"
)

func (r MirrorAuthorizationRequest) Validate(now time.Time) error {
	if r.Type != MirrorAuthorizationRequestType || !vscreenIdentity(r.RequestID) || r.Kind == "" || r.Title == "" || r.Message == "" || !utf8.ValidString(r.Title) || !utf8.ValidString(r.Message) || len([]rune(r.Title)) > 160 || len([]rune(r.Message)) > 2048 || !r.ExpiresAt.After(now) {
		return fmt.Errorf("%w: invalid authorization request", ErrInvalidMirrorDescription)
	}
	if operation := r.Operation; operation != nil {
		if operation.Target == "" || len([]rune(operation.Target)) > 2048 || len([]rune(operation.Details)) > 2048 || !utf8.ValidString(operation.Target) || !utf8.ValidString(operation.Details) {
			return fmt.Errorf("%w: incomplete operation scope", ErrInvalidMirrorDescription)
		}
		encoded, err := json.Marshal(operation)
		if err != nil || len(encoded) > 60*1024 {
			return fmt.Errorf("%w: operation scope exceeds review transport", ErrInvalidMirrorDescription)
		}
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
