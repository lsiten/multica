//go:build !darwin || !cgo

package capture

func liveResources() LiveResourceStats {
	return LiveResourceStats{Reason: "native_counter_unsupported"}
}
