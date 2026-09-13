//go:build !darwin || !cgo

package mirror

// EnsureScreenCapturePermission is a no-op off the CGO macOS build. Windows
// capture needs no consent prompt, and the unsupported-platform build never
// reaches capture (NativeCaptureSupported is false).
func EnsureScreenCapturePermission() bool {
	return true
}
