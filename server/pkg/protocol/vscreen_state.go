package protocol

import "fmt"

// VscreenState describes resource availability independently of viewer subscriptions.
type VscreenState string

const (
	VscreenStateDisabled    VscreenState = "disabled"
	VscreenStateCreating    VscreenState = "creating"
	VscreenStateReady       VscreenState = "ready"
	VscreenStateSuspended   VscreenState = "suspended"
	VscreenStateStopping    VscreenState = "stopping"
	VscreenStateUnavailable VscreenState = "unavailable"
)

// VscreenControlState describes actor authority, never optimistic client state.
type VscreenControlState string

const (
	VscreenControlIdle             VscreenControlState = "idle"
	VscreenControlAgent            VscreenControlState = "agent"
	VscreenControlAwaitingTakeover VscreenControlState = "awaiting_takeover"
	VscreenControlHuman            VscreenControlState = "human"
	VscreenControlStopping         VscreenControlState = "stopping"
)

// VscreenPermissions reports actual native permission observations, not capability advertisement.
type VscreenPermissions struct {
	ScreenRecording string `json:"screen_recording"`
	Accessibility   string `json:"accessibility"`
}

// VscreenStateSnapshot is a daemon-authoritative monotonic projection for clients.
type VscreenStateSnapshot struct {
	RuntimeID         string                   `json:"runtime_id"`
	State             VscreenState             `json:"state"`
	NativeEpoch       string                   `json:"native_epoch"`
	DisplayGeneration string                   `json:"display_generation"`
	GeometryRevision  uint64                   `json:"geometry_revision"`
	ControlState      VscreenControlState      `json:"control_state"`
	ActiveTaskID      *string                  `json:"active_task_id"`
	InterventionID    *string                  `json:"intervention_id"`
	Permissions       VscreenPermissions       `json:"permissions"`
	// HumanInteraction reports the host master switch for remote human control.
	HumanInteraction  bool                     `json:"human_interaction,omitempty"`
	ReturnReceiptID   string                   `json:"return_receipt_id,omitempty"`
	InterventionState VscreenInterventionState `json:"intervention_state,omitempty"`
	StateRevision     uint64                   `json:"state_revision"`
}

// Validate rejects absent control authority and incomplete active display generations.
func (s VscreenStateSnapshot) Validate() error {
	if !vscreenIdentity(s.RuntimeID) || s.StateRevision == 0 {
		return fmt.Errorf("%w: incomplete state identity", ErrInvalidVscreenContract)
	}
	if s.ReturnReceiptID != "" || s.InterventionState != "" {
		if s.InterventionID == nil || !vscreenIdentity(*s.InterventionID) {
			return ErrInvalidVscreenContract
		}
		switch s.InterventionState {
		case VscreenInterventionAwaitingTakeover, VscreenInterventionHuman:
			if s.ReturnReceiptID != "" {
				return ErrInvalidVscreenContract
			}
		case VscreenInterventionReadyToContinue:
			if !vscreenIdentity(s.ReturnReceiptID) {
				return ErrInvalidVscreenContract
			}
		default:
			return ErrInvalidVscreenContract
		}
	}
	switch s.State {
	case VscreenStateReady, VscreenStateSuspended, VscreenStateStopping:
		if err := (VscreenEpoch{NativeEpoch: s.NativeEpoch, DisplayGeneration: s.DisplayGeneration, GeometryRevision: s.GeometryRevision}).Validate(); err != nil {
			return err
		}
	case VscreenStateDisabled, VscreenStateCreating, VscreenStateUnavailable:
	default:
		return fmt.Errorf("%w: unknown resource state", ErrInvalidVscreenContract)
	}
	switch s.ControlState {
	case VscreenControlIdle:
		if s.ActiveTaskID != nil {
			return fmt.Errorf("%w: idle control has active task", ErrInvalidVscreenContract)
		}
	case VscreenControlAgent:
		if s.State != VscreenStateReady || s.ActiveTaskID == nil || !vscreenIdentity(*s.ActiveTaskID) {
			return fmt.Errorf("%w: agent control lacks ready resource or task", ErrInvalidVscreenContract)
		}
	case VscreenControlAwaitingTakeover, VscreenControlHuman:
		if s.InterventionID == nil || !vscreenIdentity(*s.InterventionID) {
			return fmt.Errorf("%w: missing intervention identity", ErrInvalidVscreenContract)
		}
	case VscreenControlStopping:
	default:
		return fmt.Errorf("%w: unknown control state", ErrInvalidVscreenContract)
	}
	for _, permission := range []string{s.Permissions.ScreenRecording, s.Permissions.Accessibility} {
		switch permission {
		case "unknown", "granted", "denied", "restricted", "not_determined":
		default:
			return fmt.Errorf("%w: unknown permission state", ErrInvalidVscreenContract)
		}
	}
	return nil
}
