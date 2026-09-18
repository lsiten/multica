package protocol

import "fmt"

// VscreenEnvelope binds asynchronous control traffic to the current daemon generation.
type VscreenEnvelope struct {
	WorkspaceID      string `json:"workspace_id"`
	RuntimeID        string `json:"runtime_id"`
	DaemonGeneration string `json:"daemon_generation"`
	RequestID        string `json:"request_id"`
}

// Validate rejects unscoped or uncorrelated control traffic.
func (e VscreenEnvelope) Validate() error {
	if !vscreenIdentity(e.WorkspaceID) || !vscreenIdentity(e.RuntimeID) || !vscreenIdentity(e.DaemonGeneration) || !vscreenIdentity(e.RequestID) {
		return fmt.Errorf("%w: incomplete control envelope", ErrInvalidVscreenContract)
	}
	return nil
}

// VscreenCommandKind contains remote-safe commands. Physical takeover/return stays local.
type VscreenCommandKind string

const (
	VscreenCommandEnable          VscreenCommandKind = "enable"
	VscreenCommandDisable         VscreenCommandKind = "disable"
	VscreenCommandRequestTakeover VscreenCommandKind = "request_takeover"
)

// VscreenCommand requests work; acceptance is not evidence of native completion.
type VscreenCommand struct {
	VscreenEnvelope
	CommandID string             `json:"command_id"`
	Kind      VscreenCommandKind `json:"kind"`
}

// Validate rejects empty and unknown control commands.
func (c VscreenCommand) Validate() error {
	if err := c.VscreenEnvelope.Validate(); err != nil {
		return err
	}
	if !vscreenIdentity(c.CommandID) {
		return fmt.Errorf("%w: missing command identity", ErrInvalidVscreenContract)
	}
	switch c.Kind {
	case VscreenCommandEnable, VscreenCommandDisable, VscreenCommandRequestTakeover:
		return nil
	default:
		return fmt.Errorf("%w: unknown control command", ErrInvalidVscreenContract)
	}
}

// ParseVscreenCommand compares the authenticated envelope before any command can execute.
func ParseVscreenCommand(raw []byte, current VscreenEnvelope) (VscreenCommand, error) {
	var command VscreenCommand
	if err := decodeVscreenJSON(raw, &command); err != nil {
		return command, err
	}
	if err := command.Validate(); err != nil {
		return VscreenCommand{}, err
	}
	if command.VscreenEnvelope != current {
		return VscreenCommand{}, fmt.Errorf("%w: stale or unauthorized command", ErrInvalidVscreenContract)
	}
	return command, nil
}

// VscreenCommandReceipt is queryable after HTTP timeout and identifies a native result.
type VscreenCommandReceipt struct {
	VscreenEnvelope
	CommandID string                 `json:"command_id"`
	ReceiptID string                 `json:"receipt_id"`
	State     VscreenReceiptState    `json:"state"`
	Reason    VscreenRejectionReason `json:"reason,omitempty"`
	Epoch     *VscreenEpoch          `json:"epoch,omitempty"`
}

// Validate rejects incomplete receipts and impossible status/reason combinations.
func (r VscreenCommandReceipt) Validate() error {
	if err := r.VscreenEnvelope.Validate(); err != nil {
		return err
	}
	if !vscreenIdentity(r.CommandID) || !vscreenIdentity(r.ReceiptID) {
		return fmt.Errorf("%w: incomplete command receipt", ErrInvalidVscreenContract)
	}
	if r.Epoch != nil {
		if err := r.Epoch.Validate(); err != nil {
			return err
		}
	}
	return validateVscreenReceiptState(r.State, r.Reason)
}
