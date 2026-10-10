package protocol

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ApplicationServiceGrantCapability identifies the child-only application transport contract.
const ApplicationServiceGrantCapability = "application-service-grants-v1"

// ApplicationServiceGrantRequest rotates credential authority with an explicit CAS.
// Both same-instance renewal and instance replacement advance generation; old
// sockets close and in-flight application requests must not be blindly replayed.
type ApplicationServiceGrantRequest struct {
	ServiceInstanceID  string   `json:"service_instance_id"`
	ExpectedGeneration int64    `json:"expected_generation"`
	Operations         []string `json:"operations"`
}

// ApplicationServiceGrantState contains no secret and supports lost-issuance-ACK recovery.
type ApplicationServiceGrantState struct {
	Capability        string `json:"capability"`
	WorkspaceID       string `json:"workspace_id"`
	RuntimeID         string `json:"runtime_id"`
	DaemonID          string `json:"daemon_id"`
	ServiceInstanceID string `json:"service_instance_id,omitempty"`
	Generation        int64  `json:"generation"`
	Revoked           bool   `json:"revoked"`
}

// ApplicationServiceGrantResponse returns a short-lived secret only on issuance.
type ApplicationServiceGrantResponse struct {
	ApplicationServiceGrantState
	Token      string    `json:"token"`
	Operations []string  `json:"operations"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// RevokeApplicationServiceGrantRequest cannot revoke a newer service incarnation.
type RevokeApplicationServiceGrantRequest struct {
	ServiceInstanceID string `json:"service_instance_id"`
	Generation        int64  `json:"generation"`
}

// ApplicationServiceOperationAllowed is the complete application child allowlist.
func ApplicationServiceOperationAllowed(operation string) bool {
	switch operation {
	case "sync", "claim", "observe", "result", "lease", "tunnel_control", "tunnel_data":
		return true
	default:
		return false
	}
}

// ValidApplicationDaemonID preserves textual daemon overrides while rejecting
// empty, unbounded, or control-character identities at the credential boundary.
func ValidApplicationDaemonID(value string) bool {
	if strings.TrimSpace(value) == "" || len(value) > 512 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
