package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) vscreenSnapshot(ctx context.Context, workspaceID, runtimeID string) (protocol.VscreenStateSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return protocol.VscreenStateSnapshot{}, err
	}
	ctx = context.WithoutCancel(ctx)
	key, err := d.vscreenResource(workspaceID, runtimeID)
	if err != nil {
		return protocol.VscreenStateSnapshot{}, err
	}
	s := d.vscreenRuntime()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revisions[runtimeID]++
	state := protocol.VscreenStateSnapshot{RuntimeID: runtimeID, State: protocol.VscreenStateDisabled, ControlState: protocol.VscreenControlIdle, StateRevision: s.revisions[runtimeID], Permissions: protocol.VscreenPermissions{ScreenRecording: "unknown", Accessibility: "unknown"}, HumanInteraction: d.HumanInteractionEnabled()}
	if err = d.startVscreenHost(ctx, s); err == nil {
		probeCtx, probeCancel := context.WithTimeout(ctx, time.Second)
		permissions, probeErr := s.client.ProbeAppPermissions(probeCtx)
		probeCancel()
		if probeErr == nil {
			state.Permissions.Accessibility = "denied"
			state.Permissions.ScreenRecording = "denied"
			if d.accessibilityPermissionGranted(permissions.Accessibility) {
				state.Permissions.Accessibility = "granted"
			}
			if permissions.ScreenRecording {
				state.Permissions.ScreenRecording = "granted"
			}
		}
	}
	if !s.enabled[key] {
		return state, nil
	}
	if err = d.startVscreenHost(ctx, s); err != nil {
		state.State = protocol.VscreenStateUnavailable
		return state, nil
	}
	a, err := s.manager.For(key)
	if err != nil {
		return state, err
	}
	display, err := a.Ensure(ctx)
	if err != nil {
		state.State = protocol.VscreenStateUnavailable
		return state, nil
	}
	response, err := s.client.Call(ctx, native.Request{Operation: "describe", Resource: key, Epoch: display.Epoch})
	if err != nil {
		state.State = protocol.VscreenStateUnavailable
		return state, nil
	}
	display, err = s.driver.reconcileReadback(display, response)
	if err != nil {
		return state, err
	}
	if err = a.Reconcile(display); err != nil {
		return state, err
	}
	status := a.Status()
	state.State = protocol.VscreenStateReady
	if status.Frozen {
		state.State = protocol.VscreenStateSuspended
	}
	if status.Stopping {
		state.State = protocol.VscreenStateStopping
		state.ControlState = protocol.VscreenControlStopping
	}
	if status.Lease.TaskID != "" && !status.Lease.Cancelled {
		state.ControlState = protocol.VscreenControlAgent
		state.ActiveTaskID = &status.Lease.TaskID
	}
	state.NativeEpoch = display.Epoch.NativeEpoch
	d.vscreenMu.Lock()
	reporter := d.vscreenReporter
	d.vscreenMu.Unlock()
	if reporter != nil {
		if err := reporter.InvalidateEpoch(workspaceID, runtimeID, state.NativeEpoch); err != nil {
			d.logger.Warn("virtual screen report epoch invalidation failed")
		}
	}
	state.DisplayGeneration = display.Epoch.DisplayGeneration
	state.GeometryRevision = display.Epoch.GeometryRevision
	// Keep the host permission probe authoritative. The display readback is
	// useful when the probe is unavailable, but older host helpers can report a
	// stale false value for a granted TCC identity and must not overwrite it.
	if state.Permissions.ScreenRecording != "granted" {
		state.Permissions.ScreenRecording = "denied"
		if response.Display.ScreenRecording {
			state.Permissions.ScreenRecording = "granted"
		}
	}
	d.projectVscreenIntervention(s, &state)
	return state, nil
}

// accessibilityPermissionGranted uses the same daemon-side injector that
// handles physical-screen input as a fallback for the native app-control
// preflight. macOS can attribute those two checks to different helper
// identities even when both entries are enabled in System Settings.
func (d *Daemon) accessibilityPermissionGranted(hostGranted bool) bool {
	injector := d.globalInjector()
	return accessibilityPermissionGranted(hostGranted, injector != nil && injector.Available())
}

func accessibilityPermissionGranted(hostGranted, injectorActive bool) bool {
	return hostGranted || injectorActive
}

func (d *Daemon) vscreenSources(ctx context.Context, workspaceID, runtimeID string) ([]native.SourceDescriptor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx = context.WithoutCancel(ctx)
	key, err := d.vscreenResource(workspaceID, runtimeID)
	if err != nil {
		return nil, err
	}
	s := d.vscreenRuntime()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = d.startVscreenHost(ctx, s); err != nil {
		return nil, err
	}
	return s.client.Sources(ctx, key)
}

func vscreenReason(err error) protocol.VscreenRejectionReason {
	var domain *vscreen.Error
	if errors.As(err, &domain) {
		return domain.Reason
	}
	var remote *hostclient.RemoteError
	if errors.As(err, &remote) {
		switch remote.Code {
		case "screen_recording_denied", "accessibility_denied", "permission_denied":
			return protocol.VscreenPermissionDenied
		case "stale_epoch", "stale_snapshot", "stale_window":
			return protocol.VscreenStaleSnapshot
		case "needs_intervention", "background_unsupported":
			return protocol.VscreenBackgroundUnsupported
		case "action_uncertain", "quiescence_required", "deadline_exceeded", "cancelled":
			return protocol.VscreenActionUncertainReason
		case "stale_authority", "authority_required", "lease_expired":
			return protocol.VscreenLeaseExpired
		case "app_claim_conflict":
			return protocol.VscreenAppInUse
		case "display_unavailable":
			return protocol.VscreenSourceGone
		}
	}
	return protocol.VscreenNativeUnavailable
}

func (d *Daemon) saveVscreenPreferences(s *vscreenRuntime) error {
	if d.cfg.NativeVscreenPreferencesPath == "" {
		return nil
	}
	keys := make([]protocol.ResourceKey, 0, len(s.enabled))
	for k, enabled := range s.enabled {
		if enabled {
			keys = append(keys, k)
		}
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	name := d.cfg.NativeVscreenPreferencesPath
	if err = os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".vscreen-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}
