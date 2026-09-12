//go:build cgo && darwin

package mirror

import (
	"errors"
	"testing"
)

func TestNativeCaptureCapabilityEnabledForCGODarwinBuild(t *testing.T) {
	if !NativeCaptureSupported() {
		t.Fatal("NativeCaptureSupported = false, want true for a CGO Darwin build")
	}
}

func TestDarwinCapturePermissionFailure(t *testing.T) {
	err := classifyNativeCaptureError(errors.New("cannot capture display"))
	if !errors.Is(err, ErrCapturePermissionDenied) {
		t.Fatalf("permission error = %v, want %v", err, ErrCapturePermissionDenied)
	}
	if got := ControlReasonFromError(err); got != ControlReasonPermissionDenied {
		t.Fatalf("control reason = %q, want %q", got, ControlReasonPermissionDenied)
	}
}

func TestDarwinUnexpectedCaptureFailure(t *testing.T) {
	err := classifyNativeCaptureError(errors.New("CoreGraphics failed"))
	if !errors.Is(err, ErrCaptureUnavailable) {
		t.Fatalf("unexpected capture error = %v, want %v", err, ErrCaptureUnavailable)
	}
}
