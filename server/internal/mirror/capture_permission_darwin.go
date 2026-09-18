//go:build cgo && darwin

package mirror

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework CoreGraphics
#import <CoreGraphics/CoreGraphics.h>

// CGPreflightScreenCaptureAccess returns the current Screen Recording
// authorization without prompting.
static int multica_preflight_screen_capture(void) {
	return CGPreflightScreenCaptureAccess() ? 1 : 0;
}

// CGRequestScreenCaptureAccess registers the calling executable in the
// Screen Recording TCC list and presents the system permission prompt (or
// flips the entry in System Settings). Without it, a CLI daemon that only
// calls CGWindowListCreateImage fails silently and never appears in the
// settings list, so users have no way to grant it.
static int multica_request_screen_capture(void) {
	return CGRequestScreenCaptureAccess() ? 1 : 0;
}
*/
import "C"

import "sync"

var screenCaptureRequestOnce sync.Once

// screenCaptureRequestResult records the answer to the one-time
// CGRequestScreenCaptureAccess call. Later manual grants are picked up by
// repeated preflight checks.
var screenCaptureRequestResult bool

// EnsureScreenCapturePermission reports whether the daemon already holds
// Screen Recording permission. On the first rejection per process it invokes
// the system request exactly once so the daemon shows up in the Screen
// Recording list and the host user gets the authorization prompt.
func EnsureScreenCapturePermission() bool {
	if C.multica_preflight_screen_capture() != 0 {
		return true
	}
	screenCaptureRequestOnce.Do(func() {
		screenCaptureRequestResult = C.multica_request_screen_capture() != 0
	})
	if screenCaptureRequestResult {
		return true
	}
	// The user may grant manually between attempts: re-check before giving up.
	return C.multica_preflight_screen_capture() != 0
}

// ScreenCapturePermissionGranted observes current permission without requesting consent.
func ScreenCapturePermissionGranted() bool { return C.multica_preflight_screen_capture() != 0 }
