package daemon

import (
	"context"
	"time"
)

type performanceGPUSample struct {
	ElapsedMS float64 `json:"elapsed_ms"`
	Device    float64 `json:"device_utilization_percent"`
	Renderer  float64 `json:"renderer_utilization_percent"`
	Tiler     float64 `json:"tiler_utilization_percent"`
	Memory    uint64  `json:"in_use_system_memory_bytes"`
}
type performanceSystemGPU struct {
	Scope        string                  `json:"scope"`
	Attribution  string                  `json:"attribution"`
	Method       string                  `json:"method"`
	Availability performanceAvailability `json:"availability"`
	Baseline     []performanceGPUSample  `json:"baseline"`
	Samples      []performanceGPUSample  `json:"samples"`
}

func performanceGPUBaseline(ctx context.Context) performanceSystemGPU {
	value := performanceSystemGPU{Scope: "system", Attribution: "not_benchmark_specific", Method: "IOKit AGXAccelerator PerformanceStatistics", Availability: performanceAvailability{Reason: "system_gpu_readback_unavailable"}, Baseline: []performanceGPUSample{}, Samples: []performanceGPUSample{}}
	started := time.Now()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for i := 0; i < 3; i++ {
		sample, err := performanceGPU()
		if err == nil {
			sample.ElapsedMS = float64(time.Since(started).Microseconds()) / 1000
			value.Baseline = append(value.Baseline, sample)
		}
		if i < 2 {
			select {
			case <-ctx.Done():
				return value
			case <-ticker.C:
			}
		}
	}
	value.Availability.Available = len(value.Baseline) == 3
	if value.Availability.Available {
		value.Availability.Reason = ""
	}
	return value
}
