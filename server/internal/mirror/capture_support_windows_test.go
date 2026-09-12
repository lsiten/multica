//go:build windows

package mirror

import "testing"

func TestNativeCaptureCapabilityEnabledForWindowsBuild(t *testing.T) {
	if !NativeCaptureSupported() {
		t.Fatal("NativeCaptureSupported = false, want true for Windows")
	}
}
