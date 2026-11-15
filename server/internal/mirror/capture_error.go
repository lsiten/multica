package mirror

import (
	"errors"
	"fmt"
)

const CaptureErrorMessageType = "mirror:error"

type CaptureReason string

const (
	CaptureReasonPermissionDenied   CaptureReason = "permission-denied"
	CaptureReasonUnsupported        CaptureReason = "unsupported"
	CaptureReasonNoDisplay          CaptureReason = "no-display"
	CaptureReasonCaptureUnavailable CaptureReason = "capture-unavailable"
)

type CaptureErrorMessage struct {
	Type   string        `json:"type"`
	Reason CaptureReason `json:"reason"`
}

type CaptureErrorSink interface {
	SendCaptureError(message CaptureErrorMessage) error
}

type CaptureError struct {
	Reason CaptureReason
	Err    error
}

func (e *CaptureError) Error() string {
	return e.Err.Error()
}

func (e *CaptureError) Unwrap() error {
	return e.Err
}

func captureError(reason CaptureReason, message string) *CaptureError {
	return &CaptureError{Reason: reason, Err: errors.New(message)}
}

var (
	ErrCapturePermissionDenied = captureError(
		CaptureReasonPermissionDenied,
		"mirror: screen capture permission denied",
	)
	ErrCaptureUnsupported = captureError(
		CaptureReasonUnsupported,
		"mirror: screen capture is unsupported on this platform",
	)
	ErrNoDisplay = captureError(
		CaptureReasonNoDisplay,
		"mirror: no active display",
	)
	ErrCaptureUnavailable = captureError(
		CaptureReasonCaptureUnavailable,
		"mirror: screen capture is unavailable",
	)
)

var ErrUnsupportedPlatform error = ErrCaptureUnsupported

func CaptureReasonFromError(err error) CaptureReason {
	var captureErr *CaptureError
	if errors.As(err, &captureErr) {
		return captureErr.Reason
	}
	return CaptureReasonCaptureUnavailable
}

func captureCaptureErrorMessage(err error) CaptureErrorMessage {
	return CaptureErrorMessage{
		Type:   CaptureErrorMessageType,
		Reason: CaptureReasonFromError(err),
	}
}

func wrapCaptureError(err error) error {
	if err == nil {
		return nil
	}
	var captureErr *CaptureError
	if errors.As(err, &captureErr) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrCaptureUnavailable, err)
}
