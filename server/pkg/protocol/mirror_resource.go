package protocol

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

const (
	DaemonCapabilityScreenMirrorVideoV2 = "screen-mirror-video-v2"
	DaemonCapabilityMirrorViewerGrantV1 = "mirror-viewer-grant-v1"
	EventMirrorViewerRevoke             = "mirror:viewer-revoke"
)

// ErrInvalidMirrorContract identifies malformed wire input without exposing payloads.
var ErrInvalidMirrorContract = errors.New("protocol: invalid mirror contract")

// ResourceKey identifies a runtime display across backends and login sessions.
// Native display IDs and daemon IDs are deliberately not resource identities.
type ResourceKey struct {
	BackendIdentity string `json:"backend_identity"`
	WorkspaceID     string `json:"workspace_id"`
	RuntimeID       string `json:"runtime_id"`
	UID             uint32 `json:"uid"`
	// DisplayID identifies one physical/system display within the runtime.
	DisplayID uint32 `json:"display_id,omitempty"`
}

// Validate requires a canonical backend URL and an unprivileged login identity.
func (k ResourceKey) Validate() error {
	u, err := url.Parse(k.BackendIdentity)
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("%w: invalid backend identity", ErrInvalidMirrorContract)
	}
	if u.String() != k.BackendIdentity || strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("%w: backend identity is not canonical", ErrInvalidMirrorContract)
	}
	if u.Host != strings.ToLower(u.Host) || strings.HasSuffix(u.Host, ":443") && u.Scheme == "https" || strings.HasSuffix(u.Host, ":80") && u.Scheme == "http" || u.RawPath != "" || u.Path != "" && path.Clean(u.Path) != u.Path || strings.HasSuffix(k.BackendIdentity, "/") {
		return fmt.Errorf("%w: backend identity is not canonical", ErrInvalidMirrorContract)
	}
	if !mirrorIdentity(k.WorkspaceID) || !mirrorIdentity(k.RuntimeID) || k.UID == 0 {
		return fmt.Errorf("%w: runtime login identity is incomplete", ErrInvalidMirrorContract)
	}
	return nil
}

func mirrorIdentity(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 512 && !strings.ContainsAny(value, "\x00\r\n")
}
