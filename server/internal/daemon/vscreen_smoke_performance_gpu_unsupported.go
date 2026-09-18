//go:build !darwin || !cgo

package daemon

func performanceGPU() (performanceGPUSample, error) {
	return performanceGPUSample{}, errPerformanceMetricUnavailable
}
