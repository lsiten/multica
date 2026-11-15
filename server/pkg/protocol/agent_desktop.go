package protocol

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	EventAgentDesktopReadinessQuery        = "agent-desktop:readiness-query"
	EventAgentDesktopReadinessResult       = "agent-desktop:readiness-result"
	EventAgentDesktopPermissionGuideQuery  = "agent-desktop:permission-guide-query"
	EventAgentDesktopPermissionGuideResult = "agent-desktop:permission-guide-result"
)

const (
	AgentDesktopReasonMissing                = "missing"
	AgentDesktopReasonVersionMismatch        = "version_mismatch"
	AgentDesktopReasonInvalidInstall         = "invalid_install"
	AgentDesktopReasonStatusFailed           = "status_failed"
	AgentDesktopReasonInvalidResponse        = "invalid_response"
	AgentDesktopReasonNoGraphicalSurface     = "no_graphical_surface"
	AgentDesktopReasonAccessibilityDenied    = "accessibility_denied"
	AgentDesktopReasonPermissionsCheckFailed = "permissions_check_failed"
	AgentDesktopReasonUnsupportedPlatform    = "unsupported_platform"
	AgentDesktopReasonGuideFailed            = "guide_failed"
)

type AgentDesktopPermissionState string

const (
	AgentDesktopPermissionGranted       AgentDesktopPermissionState = "granted"
	AgentDesktopPermissionDenied        AgentDesktopPermissionState = "denied"
	AgentDesktopPermissionUnknown       AgentDesktopPermissionState = "unknown"
	AgentDesktopPermissionNotRequired   AgentDesktopPermissionState = "not_required"
	AgentDesktopPermissionNotDetermined AgentDesktopPermissionState = "not_determined"
)

func (s AgentDesktopPermissionState) Valid() bool {
	switch s {
	case AgentDesktopPermissionGranted,
		AgentDesktopPermissionDenied,
		AgentDesktopPermissionUnknown,
		AgentDesktopPermissionNotRequired,
		AgentDesktopPermissionNotDetermined:
		return true
	default:
		return false
	}
}

type AgentDesktopPermissions struct {
	Accessibility   AgentDesktopPermissionState `json:"accessibility"`
	ScreenRecording AgentDesktopPermissionState `json:"screen_recording"`
	Automation      AgentDesktopPermissionState `json:"automation"`
}

type AgentDesktopReadiness struct {
	Platform     string                  `json:"platform"`
	Version      string                  `json:"version"`
	Permissions  AgentDesktopPermissions `json:"permissions"`
	SurfaceCount int                     `json:"surface_count"`
	Ready        bool                    `json:"ready"`
	Reason       string                  `json:"reason,omitempty"`
}

func (r AgentDesktopReadiness) Validate() error {
	if !r.Permissions.Accessibility.Valid() || !r.Permissions.ScreenRecording.Valid() ||
		!r.Permissions.Automation.Valid() || r.SurfaceCount < 0 || r.SurfaceCount > 256 ||
		len(r.Reason) > 80 || !utf8.ValidString(r.Reason) || strings.ContainsRune(r.Reason, 0) {
		return fmt.Errorf("%w: invalid agent-desktop readiness", ErrInvalidMirrorContract)
	}
	if r.Ready {
		if !shortProtocolText(r.Platform, 32) || !shortProtocolText(r.Version, 32) || r.Reason != "" {
			return fmt.Errorf("%w: contradictory agent-desktop readiness", ErrInvalidMirrorContract)
		}
	} else if strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("%w: unavailable agent-desktop state has no reason", ErrInvalidMirrorContract)
	}
	return nil
}

func shortProtocolText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) &&
		!strings.ContainsRune(value, 0) && utf8.RuneCountInString(value) <= max
}

type AgentDesktopReadinessQuery struct {
	RuntimeEnvelope
}

type AgentDesktopReadinessResult struct {
	RuntimeEnvelope
	Readiness AgentDesktopReadiness `json:"readiness"`
}

func (r AgentDesktopReadinessResult) Validate() error {
	if err := r.RuntimeEnvelope.Validate(); err != nil {
		return err
	}
	return r.Readiness.Validate()
}

type AgentDesktopPermissionGuideQuery struct {
	RuntimeEnvelope
}

type AgentDesktopPermissionGuideResult struct {
	RuntimeEnvelope
	Readiness AgentDesktopReadiness `json:"readiness"`
}

func (r AgentDesktopPermissionGuideResult) Validate() error {
	if err := r.RuntimeEnvelope.Validate(); err != nil {
		return err
	}
	return r.Readiness.Validate()
}
