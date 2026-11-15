package agentdesktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

var (
	capabilityOnce sync.Once
	capability     protocol.AgentDesktopReadiness
)

type readinessEnvelope struct {
	Platform    string `json:"platform"`
	Version     string `json:"version"`
	Permissions struct {
		Accessibility struct {
			State string `json:"state"`
		} `json:"accessibility"`
		ScreenRecording struct {
			State string `json:"state"`
		} `json:"screen_recording"`
		Automation struct {
			State string `json:"state"`
		} `json:"automation"`
	} `json:"permissions"`
	SupportedSurfaces []string `json:"supported_surfaces"`
}

// Ready reports whether this host can advertise agent-desktop-v1. The result
// is sampled once per daemon process; task startup still performs a fresh
// preflight so permission or installation changes fail closed.
func Ready() bool {
	return CachedReadiness().Ready
}

func CachedReadiness() protocol.AgentDesktopReadiness {
	capabilityOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		defer cancel()
		capability, _ = Inspect(ctx)
	})
	return capability
}

// Prepare validates the pinned release before a daemon advertises desktop
// readiness. Managed installs also require the complete checksum manifest and
// platform helper, while an explicit absolute override remains a development
// path that still has to pass the exact version probe.
func Prepare(ctx context.Context, path string) error {
	_, err := inspectPath(ctx, path)
	return err
}

// Inspect reports readiness without prompting for OS permissions. The returned
// state remains useful when the probe fails, so the runtime UI can show why.
func Inspect(ctx context.Context) (protocol.AgentDesktopReadiness, error) {
	path, err := Resolve()
	if err != nil {
		return failedReadiness(protocol.AgentDesktopReasonMissing), err
	}
	return inspectPath(ctx, path)
}

func inspectPath(ctx context.Context, path string) (protocol.AgentDesktopReadiness, error) {
	if err := Verify(ctx, path); err != nil {
		reason := protocol.AgentDesktopReasonInvalidInstall
		if errors.Is(err, ErrVersion) {
			reason = protocol.AgentDesktopReasonVersionMismatch
		}
		return failedReadiness(reason), err
	}
	managed, err := isManagedExecutable(path)
	if err != nil {
		return failedReadiness(protocol.AgentDesktopReasonInvalidInstall), err
	}
	if managed {
		if err := verifyInstallation(path); err != nil {
			return failedReadiness(protocol.AgentDesktopReasonInvalidInstall), err
		}
	}
	return probeReady(ctx, path)
}

// ProbeReady checks that the current graphical session can be observed. It
// never prompts for OS permissions.
func ProbeReady(ctx context.Context, path string) error {
	_, err := probeReady(ctx, path)
	return err
}

func probeReady(ctx context.Context, path string) (protocol.AgentDesktopReadiness, error) {
	raw, err := runEnvelope(ctx, path, []string{"status"}, os.Stderr)
	if err != nil {
		probeErr := &UnavailableError{Path: path, Reason: "status failed: " + executableFailure(err)}
		return failedReadiness(protocol.AgentDesktopReasonStatusFailed), probeErr
	}
	var readiness readinessEnvelope
	if err := json.Unmarshal(raw, &readiness); err != nil {
		probeErr := &UnavailableError{Path: path, Reason: "invalid status response"}
		return failedReadiness(protocol.AgentDesktopReasonInvalidResponse), probeErr
	}
	if readiness.Version != Version {
		return failedReadiness(protocol.AgentDesktopReasonVersionMismatch), &VersionError{Path: path, Got: readiness.Version}
	}
	result := protocol.AgentDesktopReadiness{
		Platform: readiness.Platform,
		Version:  readiness.Version,
		Permissions: protocol.AgentDesktopPermissions{
			Accessibility:   permissionState(readiness.Permissions.Accessibility.State),
			ScreenRecording: permissionState(readiness.Permissions.ScreenRecording.State),
			Automation:      permissionState(readiness.Permissions.Automation.State),
		},
		SurfaceCount: len(readiness.SupportedSurfaces),
	}
	if readiness.Permissions.Accessibility.State == "denied" {
		result.Reason = protocol.AgentDesktopReasonAccessibilityDenied
		return result, &UnavailableError{Path: path, Reason: "accessibility permission is denied"}
	}
	if len(readiness.SupportedSurfaces) == 0 {
		result.Reason = protocol.AgentDesktopReasonNoGraphicalSurface
		return result, &UnavailableError{Path: path, Reason: "no supported accessibility surface is available"}
	}
	if _, err := runEnvelope(ctx, path, []string{"permissions"}, os.Stderr); err != nil {
		result.Reason = protocol.AgentDesktopReasonPermissionsCheckFailed
		return result, &UnavailableError{Path: path, Reason: "permissions check failed: " + executableFailure(err)}
	}
	result.Ready = true
	return result, nil
}

// Probe verifies the pinned binary and checks that the current graphical
// session can be observed. It never prompts for OS permissions.
func Probe(ctx context.Context, path string) error {
	if err := Verify(ctx, path); err != nil {
		return err
	}
	return ProbeReady(ctx, path)
}

// OpenPermissionGuide starts the pinned binary's nonprompting System Settings
// guide. The command's only effect is opening the OS permission surface.
func OpenPermissionGuide(ctx context.Context) error {
	path, err := Resolve()
	if err != nil {
		return err
	}
	if err := Verify(ctx, path); err != nil {
		return err
	}
	managed, err := isManagedExecutable(path)
	if err != nil {
		return err
	}
	if managed {
		if err := verifyInstallation(path); err != nil {
			return err
		}
	}
	_, err = runEnvelope(ctx, path, []string{"permissions", "--guide"}, os.Stderr)
	return err
}

func failedReadiness(reason string) protocol.AgentDesktopReadiness {
	return protocol.AgentDesktopReadiness{
		Permissions: protocol.AgentDesktopPermissions{
			Accessibility:   protocol.AgentDesktopPermissionUnknown,
			ScreenRecording: protocol.AgentDesktopPermissionUnknown,
			Automation:      protocol.AgentDesktopPermissionUnknown,
		},
		Reason: reason,
	}
}

func permissionState(state string) protocol.AgentDesktopPermissionState {
	value := protocol.AgentDesktopPermissionState(strings.TrimSpace(state))
	switch value {
	case protocol.AgentDesktopPermissionGranted,
		protocol.AgentDesktopPermissionDenied,
		protocol.AgentDesktopPermissionUnknown,
		protocol.AgentDesktopPermissionNotRequired,
		protocol.AgentDesktopPermissionNotDetermined:
		return value
	default:
		return protocol.AgentDesktopPermissionUnknown
	}
}

func isManagedExecutable(path string) (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, &UnavailableError{Path: path, Reason: fmt.Sprintf("resolve home: %v", err)}
	}
	managed, managedErr := filepath.Abs(ManagedExecutablePath(home))
	if managedErr != nil {
		return false, managedErr
	}
	absolute, absoluteErr := filepath.Abs(path)
	if absoluteErr != nil {
		return false, absoluteErr
	}
	return filepath.Clean(absolute) == filepath.Clean(managed), nil
}
