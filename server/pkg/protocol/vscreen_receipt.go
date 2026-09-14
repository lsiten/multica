package protocol

import "fmt"

// VscreenRejectionReason is the fixed, non-sensitive input refusal taxonomy.
type VscreenRejectionReason string

const (
	VscreenPermissionDenied      VscreenRejectionReason = "permission_denied"
	VscreenBackgroundUnsupported VscreenRejectionReason = "background_unsupported"
	VscreenAppInUse              VscreenRejectionReason = "app_in_use"
	VscreenWindowMoved           VscreenRejectionReason = "window_moved"
	VscreenStaleSnapshot         VscreenRejectionReason = "stale_snapshot"
	VscreenLeaseExpired          VscreenRejectionReason = "lease_expired"
	VscreenSourceGone            VscreenRejectionReason = "source_gone"
	VscreenActionUncertainReason VscreenRejectionReason = "action_uncertain"
	VscreenNativeUnavailable     VscreenRejectionReason = "native_unavailable"
	VscreenResumeUnavailable     VscreenRejectionReason = "resume_unavailable"
)

// Valid reports whether a refusal is a supported public reason.
func (r VscreenRejectionReason) Valid() bool {
	switch r {
	case VscreenPermissionDenied, VscreenBackgroundUnsupported, VscreenAppInUse, VscreenWindowMoved, VscreenStaleSnapshot, VscreenLeaseExpired, VscreenSourceGone, VscreenActionUncertainReason, VscreenNativeUnavailable, VscreenResumeUnavailable:
		return true
	default:
		return false
	}
}

// VscreenReceiptState distinguishes transport acceptance from actual execution.
type VscreenReceiptState string

const (
	VscreenReceiptPending   VscreenReceiptState = "pending"
	VscreenReceiptRunning   VscreenReceiptState = "running"
	VscreenReceiptSucceeded VscreenReceiptState = "succeeded"
	VscreenReceiptFailed    VscreenReceiptState = "failed"
	VscreenReceiptUnknown   VscreenReceiptState = "unknown"
)

// VscreenActionOutcome separates native dispatch from subsequent observation-based verification.
type VscreenActionOutcome string

const (
	VscreenActionDispatched VscreenActionOutcome = "dispatched"
	VscreenActionVerified   VscreenActionOutcome = "verified"
	VscreenActionUncertain  VscreenActionOutcome = "uncertain"
)

// VscreenActionReceipt is cached by action ID. Unknown outcomes must never be replayed automatically.
type VscreenActionReceipt struct {
	Target   VscreenActionTarget    `json:"target"`
	ActionID string                 `json:"action_id"`
	Sequence uint64                 `json:"sequence"`
	State    VscreenReceiptState    `json:"state"`
	Outcome  VscreenActionOutcome   `json:"outcome,omitempty"`
	Reason   VscreenRejectionReason `json:"reason,omitempty"`
}

// Validate rejects default receipts and claims that an uncertain action succeeded.
func (r VscreenActionReceipt) Validate() error {
	if err := r.Target.Validate(); err != nil {
		return err
	}
	if !vscreenIdentity(r.ActionID) || r.Sequence == 0 {
		return fmt.Errorf("%w: missing receipt identity", ErrInvalidVscreenContract)
	}
	if err := validateVscreenReceiptState(r.State, r.Reason); err != nil {
		return err
	}
	switch r.State {
	case VscreenReceiptPending, VscreenReceiptRunning, VscreenReceiptFailed:
		if r.Outcome == "" {
			return nil
		}
	case VscreenReceiptSucceeded:
		if r.Outcome == VscreenActionDispatched || r.Outcome == VscreenActionVerified {
			return nil
		}
	case VscreenReceiptUnknown:
		if r.Outcome == VscreenActionUncertain && r.Reason == VscreenActionUncertainReason {
			return nil
		}
	default:
		return fmt.Errorf("%w: unknown receipt state", ErrInvalidVscreenContract)
	}
	return fmt.Errorf("%w: receipt outcome contradicts state", ErrInvalidVscreenContract)
}

func validateVscreenReceiptState(state VscreenReceiptState, reason VscreenRejectionReason) error {
	switch state {
	case VscreenReceiptPending, VscreenReceiptRunning, VscreenReceiptSucceeded:
		if reason == "" {
			return nil
		}
	case VscreenReceiptFailed, VscreenReceiptUnknown:
		if reason.Valid() {
			return nil
		}
	default:
		return fmt.Errorf("%w: unknown receipt state", ErrInvalidVscreenContract)
	}
	return fmt.Errorf("%w: receipt reason contradicts state", ErrInvalidVscreenContract)
}
