//go:build !darwin || !cgo

package daemon

// Platforms without a native resource sampler (Linux/Windows, or a macOS build
// without cgo) report no reading; the panel degrades to "—" rather than a
// wrong zero. A native Linux /proc sampler can replace this without changing
// the caller.
func sampleProcessResources(pid int) (processResourceReading, bool) {
	return processResourceReading{}, false
}
