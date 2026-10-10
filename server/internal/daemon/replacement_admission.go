package daemon

import (
	"fmt"
	"strings"
)

// OldTreeExitProof is the actual evidence the prior execution has exited. A
// policy/permission deadline, an expired grant, an offline runtime, a failed
// status or a cancellation RPC ACK is never exit proof: those are owner-side
// states, not proof the provider process tree and its participant/root have
// actually exited. Only real provider/worker exit plus participant/root release
// qualify.
type OldTreeExitProof struct {
	// ProviderTreeExited is true only when the prior provider/worker and its
	// process tree have actually exited (confirmed, not inferred from a policy
	// failure, an expired grant, an offline runtime, a failed status or a
	// cancellation ACK).
	ProviderTreeExited bool
	// ParticipantReleased is true only when the prior participant/root use has
	// actually been released (a real release, not a policy/permission timeout).
	ParticipantReleased bool
}

// ReplacementAdmission is the owner's (control until F3, the task worker
// afterwards) decision to admit a new server-authorized claim onto a resource
// whose prior execution may still be running. It is the minimum typed boundary
// for the F3/G task14 cut: a new claim may be server-authorized while the prior
// local tree is still unknown, but local admission must block conflicting
// execution/root use until actual provider/worker exit plus participant/root
// proof is available. A policy/permission deadline, expired grant, offline
// runtime, failed status or a cancellation RPC ACK is NOT old-tree exit evidence
// and must never admit a replacement or "adopt" a policy-failed old worker as a
// new claim.
type ReplacementAdmission struct {
	// TaskID and RuntimeID bind the admission to the exact resource.
	TaskID    string
	RuntimeID string
	// Admitted is the owner's decision: true allows the new claim onto the
	// resource, false blocks it. It is never a promise of the old tree's state.
	Admitted bool
	// Exit is the actual old-tree exit proof; it is nil unless Admitted is true.
	Exit *OldTreeExitProof
	// ReasonCode explains a refusal (ReplacementReasonOldTreeUnknown,
	// ReplacementReasonOldTreeRunning or ReplacementReasonNotReleased); it is
	// empty when Admitted is true. It is evidence, never authorization.
	ReasonCode string
}

// Refusal reasons. They are the closed set the owner emits on refusal; a
// replacement must not be admitted on any code outside this set.
const (
	// ReplacementReasonOldTreeUnknown: the prior tree is still unknown; there
	// is no exit proof to admit a replacement.
	ReplacementReasonOldTreeUnknown = "old_tree_unknown"
	// ReplacementReasonOldTreeRunning: the prior provider/worker is still
	// running (a policy/permission/cancellation state, never exit proof).
	ReplacementReasonOldTreeRunning = "old_tree_running"
	// ReplacementReasonNotReleased: the provider tree exited but the participant/
	// root use has not actually been released, so conflicting use must be blocked.
	ReplacementReasonNotReleased = "participant_not_released"
)

// Validate enforces the task14 boundary. It fails closed: an empty task/runtime
// id, an admission without full old-tree exit proof, a refusal carrying exit
// proof, or a refusal with an unknown reason is rejected. A policy/permission
// deadline, expired grant, offline runtime, failed status or cancellation ACK
// is never exit proof, so it can never admit a replacement.
func (a ReplacementAdmission) Validate() error {
	if strings.TrimSpace(a.TaskID) == "" {
		return fmt.Errorf("replacement admission has no task id")
	}
	if strings.TrimSpace(a.RuntimeID) == "" {
		return fmt.Errorf("replacement admission has no runtime id")
	}
	if a.Admitted {
		if a.Exit == nil || !a.Exit.ProviderTreeExited || !a.Exit.ParticipantReleased {
			return fmt.Errorf("replacement admission is admitted without full old-tree exit proof")
		}
		if strings.TrimSpace(a.ReasonCode) != "" {
			return fmt.Errorf("replacement admission is admitted but carries a refusal reason %q", a.ReasonCode)
		}
		return nil
	}
	if a.Exit != nil {
		return fmt.Errorf("replacement admission is refused but carries old-tree exit proof")
	}
	if !knownReplacementReason(a.ReasonCode) {
		return fmt.Errorf("replacement admission is refused but carries an unknown reason %q", a.ReasonCode)
	}
	return nil
}

func knownReplacementReason(code string) bool {
	switch code {
	case ReplacementReasonOldTreeUnknown, ReplacementReasonOldTreeRunning, ReplacementReasonNotReleased:
		return true
	default:
		return false
	}
}

// AdmitReplacement is the owner-side boundary that produces one replacement
// admission. It is NOT the hot path: the cross-process admission lands in F3.
// Only actual provider/worker exit plus participant/root release admits a
// replacement; anything else (a policy/permission deadline, an expired grant, an
// offline runtime, a failed status or a cancellation ACK) is not exit proof and
// refuses rather than "adopting" a policy-failed old worker as a new claim.
func AdmitReplacement(taskID, runtimeID string, exit *OldTreeExitProof) ReplacementAdmission {
	a := ReplacementAdmission{TaskID: taskID, RuntimeID: runtimeID}
	if exit != nil && exit.ProviderTreeExited && exit.ParticipantReleased {
		// Only real exit proof admits; copy it so the admission is immutable.
		full := OldTreeExitProof{ProviderTreeExited: true, ParticipantReleased: true}
		a.Admitted = true
		a.Exit = &full
		return a
	}
	a.Admitted = false
	if exit != nil && exit.ProviderTreeExited && !exit.ParticipantReleased {
		a.ReasonCode = ReplacementReasonNotReleased
	} else {
		a.ReasonCode = ReplacementReasonOldTreeUnknown
	}
	return a
}
