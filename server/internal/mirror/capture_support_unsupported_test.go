//go:build !windows && !(cgo && darwin)

package mirror

import (
	"context"
	"errors"
	"testing"
)

func TestNativeCaptureCapabilityMatchesUnsupportedBuild(t *testing.T) {
	if NativeCaptureSupported() {
		t.Fatal("NativeCaptureSupported = true, want false for this build")
	}
	_, err := (NativeCapturer{}).Capture(context.Background())
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("capture error = %v, want %v", err, ErrUnsupportedPlatform)
	}
}
