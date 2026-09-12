//go:build cgo && darwin

package mirror

// NativeCaptureSupported reports whether this build includes the native
// CoreGraphics-backed capturer. macOS screen capture requires CGO.
func NativeCaptureSupported() bool {
	return true
}
