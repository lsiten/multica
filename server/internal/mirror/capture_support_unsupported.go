//go:build !windows && !(cgo && darwin)

package mirror

// NativeCaptureSupported reports that native capture is unavailable in this
// build. Linux is intentionally unsupported, and a CGO-free macOS build links
// the screenshot package's unsupported stub.
func NativeCaptureSupported() bool {
	return false
}
