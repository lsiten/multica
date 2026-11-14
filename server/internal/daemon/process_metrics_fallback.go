//go:build (darwin && !cgo) || (!darwin && !linux && !windows)

package daemon

// Platforms without a native resource sampler report no reading; the panel
// degrades to "—" rather than a wrong zero. Native samplers exist for macOS,
// Linux, and Windows.
func sampleProcessResources(pid int) (processResourceReading, bool) {
	return processResourceReading{}, false
}

// On a platform without a native tree sampler the tree reading is not available,
// so the panel degrades the aggregate to "—" rather than showing a wrong zero.
func sampleProcessTreeResources(rootPID int) (processResourceReading, int, bool) {
	return processResourceReading{}, 0, false
}

// isMulticaAppProcess is used to decide whether to walk the desktop app's tree
// on platforms with a native sampler; unsupported platforms do not attempt it.
func isMulticaAppProcess(pid int) bool {
	return false
}
