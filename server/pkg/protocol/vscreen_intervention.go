package protocol

import (
	"fmt"
	"unicode/utf8"
)

// VscreenInterventionState tracks a stopped run and a separately linked continuation.
type VscreenInterventionState string

const (
	VscreenInterventionAwaitingTakeover VscreenInterventionState = "awaiting_takeover"
	VscreenInterventionHuman            VscreenInterventionState = "human"
	VscreenInterventionReadyToContinue  VscreenInterventionState = "ready_to_continue"
	VscreenInterventionContinued        VscreenInterventionState = "continued"
	VscreenInterventionCancelled        VscreenInterventionState = "cancelled"
	VscreenInterventionStale            VscreenInterventionState = "stale"
)

// VscreenIntervention never means a live provider run can simply resume after an MCP wait.
type VscreenIntervention struct {
	VscreenEnvelope
	InterventionID     string                   `json:"intervention_id"`
	AgentID            string                   `json:"agent_id"`
	SourceTaskID       string                   `json:"source_task_id"`
	ContinuationTaskID string                   `json:"continuation_task_id,omitempty"`
	Reason             VscreenRejectionReason   `json:"reason"`
	State              VscreenInterventionState `json:"state"`
	Epoch              VscreenEpoch             `json:"epoch"`
	ReturnReceiptID    string                   `json:"return_receipt_id,omitempty"`
	LastActionID       string                   `json:"last_action_id,omitempty"`
	HumanSummary       string                   `json:"human_summary"`
}

// Validate checks wire shape; the service must authenticate and consume native return proof atomically.
func (i VscreenIntervention) Validate() error {
	if err := i.VscreenEnvelope.Validate(); err != nil {
		return err
	}
	if err := i.Epoch.Validate(); err != nil {
		return err
	}
	if !vscreenIdentity(i.InterventionID) || !vscreenIdentity(i.AgentID) || !vscreenIdentity(i.SourceTaskID) || !i.Reason.Valid() || len(i.HumanSummary) > 2048 || !utf8.ValidString(i.HumanSummary) {
		return fmt.Errorf("%w: incomplete intervention", ErrInvalidVscreenContract)
	}
	if i.State != VscreenInterventionContinued && i.ContinuationTaskID != "" {
		return fmt.Errorf("%w: continuation exists before consumption", ErrInvalidVscreenContract)
	}
	switch i.State {
	case VscreenInterventionAwaitingTakeover, VscreenInterventionHuman, VscreenInterventionCancelled, VscreenInterventionStale:
		return nil
	case VscreenInterventionReadyToContinue:
		if vscreenIdentity(i.ReturnReceiptID) {
			return nil
		}
	case VscreenInterventionContinued:
		if vscreenIdentity(i.ReturnReceiptID) && vscreenIdentity(i.ContinuationTaskID) && i.ContinuationTaskID != i.SourceTaskID {
			return nil
		}
	default:
		return fmt.Errorf("%w: unknown intervention state", ErrInvalidVscreenContract)
	}
	return fmt.Errorf("%w: continuation lacks return receipt or new task", ErrInvalidVscreenContract)
}
