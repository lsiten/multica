package vscreen

import "github.com/multica-ai/multica/server/pkg/protocol"

// Error carries a public refusal and retains a private diagnostic cause for errors.Is/As.
// Error text deliberately excludes input contents, process titles, and secrets in the cause.
type Error struct {
	Reason protocol.VscreenRejectionReason
	Cause  error
}

func (e *Error) Error() string { return "vscreen: " + string(e.Reason) }
func (e *Error) Unwrap() error { return e.Cause }
