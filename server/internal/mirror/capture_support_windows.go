//go:build windows

package mirror

// NativeCaptureSupported reports whether the GDI-backed Windows capturer is
// compiled into the daemon.
func NativeCaptureSupported() bool {
	return true
}
