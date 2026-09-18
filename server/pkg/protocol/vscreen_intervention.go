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

// VscreenContinuationContext is server-protected claim metadata for a new linked run.
// The daemon must acquire a new task capability and observe the returned screen before acting.
type VscreenContinuationContext struct {
	InterventionID  string       `json:"intervention_id"`
	SourceTaskID    string       `json:"source_task_id"`
	HumanSummary    string       `json:"human_summary"`
	FreshSession    bool         `json:"fresh_session"`
	Epoch           VscreenEpoch `json:"epoch"`
	ReturnReceiptID string       `json:"return_receipt_id"`
}

// DaemonGenerationHeader exposes the authenticated socket generation at upgrade.
const DaemonGenerationHeader = "X-Daemon-Generation"

const EventVscreenInterventionAck = "vscreen:intervention-ack"

// VscreenInterventionAck confirms persistence, never native execution. Producers
// match the entire envelope on the current socket and retry unacknowledged reports
// with the same request ID and native proof after refreshing the socket generation.
type VscreenInterventionAck struct {
	VscreenEnvelope
	InterventionID string `json:"intervention_id"`
	Accepted       bool   `json:"accepted"`
	Version        int64  `json:"version,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

// Validate rejects ambiguous acknowledgements and unknown rejection text.
func (a VscreenInterventionAck) Validate() error {
	if err := a.VscreenEnvelope.Validate(); err != nil {
		return err
	}
	if !vscreenIdentity(a.InterventionID) {
		return ErrInvalidVscreenContract
	}
	if a.Accepted {
		if a.Version > 0 && a.Reason == "" {
			return nil
		}
		return ErrInvalidVscreenContract
	}
	if a.Version != 0 {
		return ErrInvalidVscreenContract
	}
	switch a.Reason {
	case "invalid_report", "permission_denied", "stale_generation", "daemon_unavailable", "daemon_timeout", "request_capacity", "not_found", "pending_conflict", "source_mismatch", "source_not_stopped", "invalid_transition", "report_replayed", "intervention_failed":
		return nil
	default:
		return ErrInvalidVscreenContract
	}
}
