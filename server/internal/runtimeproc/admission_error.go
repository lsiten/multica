package runtimeproc

import "errors"

// NotSubmittedError proves that local validation rejected a request before any I/O.
type NotSubmittedError struct{ Cause error }

func (e *NotSubmittedError) Error() string { return e.Cause.Error() }
func (e *NotSubmittedError) Unwrap() error { return e.Cause }

// RejectedBeforeAdmission classifies submission errors, never Receipt.Error or
// query failures. Only these failures permit a fresh request without reconciliation.
func RejectedBeforeAdmission(err error) bool {
	var local *NotSubmittedError
	if errors.As(err, &local) {
		return true
	}
	var rejection *Error
	if !errors.As(err, &rejection) {
		return false
	}
	switch rejection.Code {
	case "unauthorized", "identity_mismatch", "malformed", "payload_too_large", "unknown_operation", "draining", "stopped", "deadline", "request_conflict", "resource_exhausted", "stale_fence", "retired_request":
		return true
	default:
		return false
	}
}
