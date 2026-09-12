package mirror

import (
	"errors"
	"fmt"
)

const ControlMessageTypeError = "mirror:error"

type ControlReason string

const (
	ControlReasonPermissionDenied   ControlReason = "permission-denied"
	ControlReasonUnsupported        ControlReason = "unsupported"
	ControlReasonNoDisplay          ControlReason = "no-display"
	ControlReasonCaptureUnavailable ControlReason = "capture-unavailable"
)

type ControlMessage struct {
	Type   string        `json:"type"`
	Reason ControlReason `json:"reason"`
}

type ControlSink interface {
	SendControl(message ControlMessage) error
}

type CaptureError struct {
	Reason ControlReason
	Err    error
}

func (e *CaptureError) Error() string {
	return e.Err.Error()
}

func (e *CaptureError) Unwrap() error {
	return e.Err
}

func captureError(reason ControlReason, message string) *CaptureError {
	return &CaptureError{Reason: reason, Err: errors.New(message)}
}

var (
	ErrCapturePermissionDenied = captureError(
		ControlReasonPermissionDenied,
		"mirror: screen capture permission denied",
	)
	ErrCaptureUnsupported = captureError(
		ControlReasonUnsupported,
		"mirror: screen capture is unsupported on this platform",
	)
	ErrNoDisplay = captureError(
		ControlReasonNoDisplay,
		"mirror: no active display",
	)
	ErrCaptureUnavailable = captureError(
		ControlReasonCaptureUnavailable,
		"mirror: screen capture is unavailable",
	)
)

var ErrUnsupportedPlatform error = ErrCaptureUnsupported

func ControlReasonFromError(err error) ControlReason {
	var captureErr *CaptureError
	if errors.As(err, &captureErr) {
		return captureErr.Reason
	}
	return ControlReasonCaptureUnavailable
}

func captureControlMessage(err error) ControlMessage {
	return ControlMessage{
		Type:   ControlMessageTypeError,
		Reason: ControlReasonFromError(err),
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
