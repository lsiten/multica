// Package appcontrol owns native app windows and guarded background operations.
package appcontrol

import (
	"context"
	"math"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Error exposes only a fixed native refusal; input text and OS diagnostics are omitted.
type Error struct{ Reason string }

func (e *Error) Error() string    { return "appcontrol: " + e.Reason }
func refusal(reason string) error { return &Error{Reason: reason} }

// Bounds uses global Quartz points, never viewer CSS pixels.
type Bounds struct{ X, Y, Width, Height float64 }

func (b Bounds) valid() bool {
	for _, n := range []float64{b.X, b.Y, b.Width, b.Height} {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
	}
	return b.Width > 0 && b.Height > 0
}

// Contains requires the complete window to lie inside the authorized display.
func (b Bounds) Contains(w Bounds) bool {
	return b.valid() && w.valid() && w.X >= b.X && w.Y >= b.Y && w.X+w.Width <= b.X+b.Width && w.Y+w.Height <= b.Y+b.Height
}

// Authority is supplied by the authenticated parent, not accepted as an Agent PID target.
type Authority struct {
	Resource              protocol.ResourceKey
	Epoch                 protocol.VscreenEpoch
	TaskID, TransactionID string
	LeaseEpoch            uint64
	ObserverGrant         string
}

// Access tells the host verifier whether a live GUI lease or a read-only grant is required.
type Access string

const (
	ObserveAccess Access = "observe"
	ControlAccess Access = "control"
)

// Display must come from the native registry and be revalidated by Authorize on each operation.
type Display struct {
	Resource protocol.ResourceKey
	Epoch    protocol.VscreenEpoch
	ID       uint32
	Bounds   Bounds
	Virtual  bool
}

// Process is independently read from proc_pidinfo and NSRunningApplication.
// Optional code metadata is populated only by the private PID-policy identity read.
type Process struct {
	PID                                                       int
	UID                                                       uint32
	Start, BundleID, OSBuild                                  string
	ExecutablePath, SigningID, CodeHash, AppVersion, AppBuild string
}

// Window binds an opaque handle to an OS process incarnation and window identifier.
type Window struct {
	Handle                 string
	Process                Process
	WindowID, DisplayID    uint32
	Bounds, OriginalBounds Bounds
	SnapshotRevision       uint64
}

// Element is one bounded accessibility node from the current observation.
type Element struct {
	Handle, Role, Title, Value string
	Bounds                     Bounds
	Press, SetValue            bool
}

// Observation includes a bounded AX tree and optionally an owned PNG image.
type Observation struct {
	// PIDInputCertificationConfigured is policy availability, not support for a particular action.
	PIDInputCertificationConfigured bool
	// PIDInputCompletionAvailable reports verifier wiring, not a per-action acknowledgement.
	PIDInputCompletionAvailable bool
	// PIDInputVerification is none or verified_variants; an exact action still needs authorization.
	PIDInputVerification string
	Display              Display
	Window               Window
	Elements             []Element
	PNG                  []byte
	Width, Height        uint32
	Truncated            bool
}

// LaunchRequest permits only an installed bundle ID and explicit local file paths.
type LaunchRequest struct {
	BundleID string
	Files    []string
}

// HumanRequest requires a separately minted local Desktop grant; GUI leases cannot substitute.
type HumanRequest struct {
	Grant, WindowHandle, Direction string
	InterventionID                 string
	Resource                       protocol.ResourceKey
}

// Result distinguishes an observed effect from mere delivery or uncertainty.
type Result struct {
	Outcome            protocol.VscreenActionOutcome
	Mechanism          string
	CompletionVerified bool
}

// Config callbacks are native-host obligations: authenticate/fence the parent connection,
// registry epochs, task/transaction and lease generations. They do not query a daemon Actor
// across processes. AuthorizeHuman must consume a separate local owner grant after quiescence.
type Config struct {
	Authorize      func(context.Context, Authority, Access) (Display, error)
	AuthorizeHuman func(context.Context, HumanRequest) (Display, error)
	// CertifiedPIDInput is populated only from an independently verified app/OS/action matrix.
	// Production hosts use only the verified policy. An isolated qualification factory may
	// install invocation-scoped test-fixture checks without advertising production verification.
	// Nil denies per-PID input while retaining semantic AX actions.
	CertifiedPIDInput func(Process, protocol.VscreenAction) PIDInputDecision
	// VerifyPIDCompletion is code-owned; no receipt or completion switch is accepted over RPC.
	VerifyPIDCompletion func(context.Context, PIDCompletion) (PIDCompletionReceipt, error)
	// PIDInputVerification reports evidence presence independently of hook configuration.
	PIDInputVerification func(Process) string
}

// Permissions reports current TCC state. Probe reads it without prompting;
// Request prompts through the selected system consent flows.
type Permissions struct{ Accessibility, ScreenRecording bool }

// PermissionRequest selects which system consent flows should be presented.
type PermissionRequest struct{ Accessibility, ScreenRecording bool }
