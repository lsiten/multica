//go:build !darwin || !cgo

package daemon

func performanceClockNS() uint64 { return 0 }
func performanceProcess(int) (performanceProcessReading, error) {
	return performanceProcessReading{}, errPerformanceMetricUnavailable
}
