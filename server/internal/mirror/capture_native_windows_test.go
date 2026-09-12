//go:build windows

package mirror

import (
	"errors"
	"testing"
)

func TestWindowsGDIFailuresMapToCaptureUnavailable(t *testing.T) {
	for _, message := range []string{
		"GetDC failed",
		"CreateCompatibleDC failed",
		"CreateCompatibleBitmap failed",
		"SelectObject failed",
		"BitBlt failed",
		"GetDIBits failed",
	} {
		t.Run(message, func(t *testing.T) {
			err := classifyNativeCaptureError(errors.New(message))
			if !errors.Is(err, ErrCaptureUnavailable) {
				t.Fatalf("classifyNativeCaptureError(%q) = %v, want %v", message, err, ErrCaptureUnavailable)
			}
			if got := ControlReasonFromError(err); got != ControlReasonCaptureUnavailable {
				t.Fatalf("control reason = %q, want %q", got, ControlReasonCaptureUnavailable)
			}
		})
	}
}
