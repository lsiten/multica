//go:build !darwin || !cgo

package daemon

// Platforms without a native resource sampler (Linux/Windows, or a macOS build
// without cgo) report no reading; the panel degrades to "—" rather than a
// wrong zero. A native Linux /proc sampler can replace this without changing
// the caller.
func sampleProcessResources(pid int) (processResourceReading, bool) {
	return processResourceReading{}, false
}

// On a platform without a native tree sampler the tree reading is not available,
// so the panel degrades the aggregate to "—" rather than showing a wrong zero.
func sampleProcessTreeResources(rootPID int) (processResourceReading, int, bool) {
	return processResourceReading{}, 0, false
}

// isMulticaAppProcess is only used to decide whether to walk the desktop app's
// tree on macOS; on other platforms the walk is not attempted.
func isMulticaAppProcess(pid int) bool {
	return false
}
