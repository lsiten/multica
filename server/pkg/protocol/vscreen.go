package protocol

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

const (
	DaemonCapabilityVirtualScreenV1     = "virtual-screen-v1"
	DaemonCapabilityBackgroundInputV1   = "background-input-v1"
	DaemonCapabilityScreenMirrorVideoV2 = "screen-mirror-video-v2"
	DaemonCapabilityMirrorViewerGrantV1 = "mirror-viewer-grant-v1"
	EventVscreenCommand                 = "vscreen:command"
	EventVscreenResult                  = "vscreen:result"
	EventVscreenState                   = "vscreen:state"
	EventVscreenIntervention            = "vscreen:intervention"
	EventMirrorViewerRevoke             = "mirror:viewer-revoke"
	VscreenPauseReasonHumanIntervention = "gui_human_intervention"
)

// ErrInvalidVscreenContract identifies malformed wire input, without exposing payloads.
var ErrInvalidVscreenContract = errors.New("protocol: invalid virtual screen contract")

// ResourceKey identifies a runtime display across backends and login sessions.
// Native display IDs and daemon IDs are deliberately not resource identities.
type ResourceKey struct {
	BackendIdentity string `json:"backend_identity"`
	WorkspaceID     string `json:"workspace_id"`
	RuntimeID       string `json:"runtime_id"`
	UID             uint32 `json:"uid"`
	// DisplayID identifies one physical/system display within the runtime. It is
	// zero for the managed virtual display, where UID owns the resource.
	DisplayID uint32 `json:"display_id,omitempty"`
}

// Validate requires a canonical backend URL and an unprivileged login identity.
func (k ResourceKey) Validate() error {
	u, err := url.Parse(k.BackendIdentity)
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("%w: invalid backend identity", ErrInvalidVscreenContract)
	}
	if u.String() != k.BackendIdentity || strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("%w: backend identity is not canonical", ErrInvalidVscreenContract)
	}
	if u.Host != strings.ToLower(u.Host) || strings.HasSuffix(u.Host, ":443") && u.Scheme == "https" || strings.HasSuffix(u.Host, ":80") && u.Scheme == "http" || u.RawPath != "" || u.Path != "" && path.Clean(u.Path) != u.Path || strings.HasSuffix(k.BackendIdentity, "/") {
		return fmt.Errorf("%w: backend identity is not canonical", ErrInvalidVscreenContract)
	}
	if !vscreenIdentity(k.WorkspaceID) || !vscreenIdentity(k.RuntimeID) || k.UID == 0 {
		return fmt.Errorf("%w: runtime login identity is incomplete", ErrInvalidVscreenContract)
	}
	return nil
}

// VscreenEpoch invalidates targets whenever the native host, display, or geometry changes.
type VscreenEpoch struct {
	NativeEpoch       string `json:"native_epoch"`
	DisplayGeneration string `json:"display_generation"`
	GeometryRevision  uint64 `json:"geometry_revision"`
}

// Validate rejects zero epochs; a missing revision never grants control.
func (e VscreenEpoch) Validate() error {
	if !vscreenIdentity(e.NativeEpoch) || !vscreenIdentity(e.DisplayGeneration) || e.GeometryRevision == 0 {
		return fmt.Errorf("%w: display epoch is incomplete", ErrInvalidVscreenContract)
	}
	return nil
}

func vscreenIdentity(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 512 && !strings.ContainsAny(value, "\x00\r\n")
}
