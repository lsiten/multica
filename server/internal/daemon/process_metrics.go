package daemon

import (
	"errors"
	"fmt"
)

// ProcessMetrics is a point-in-time resource reading for one OS process. It is
// local-only diagnostic data: it never leaves the daemon and carries no secret.
// A reading is best-effort — any single field that cannot be sampled is left
// zero and Available is false so a caller can show "—" rather than a wrong 0.
type ProcessMetrics struct {
	PID         int    `json:"pid"`
	MemoryBytes uint64 `json:"memory_bytes,omitempty"`
	CPUTimeMS   uint64 `json:"cpu_time_ms,omitempty"`
	Available   bool   `json:"available"`
}

// sampleProcessMetrics returns a best-effort resource reading for pid. It never
// fails hard: a process that vanished or a platform without a sampler returns
// an empty reading with Available=false, so the panel degrades to "—".
func sampleProcessMetrics(pid int) ProcessMetrics {
	if pid <= 0 {
		return ProcessMetrics{PID: pid, Available: false}
	}
	reading, ok := sampleProcessResources(pid)
	return ProcessMetrics{
		PID:         pid,
		MemoryBytes: reading.memoryBytes,
		CPUTimeMS:   reading.cpuTimeMS,
		Available:   ok,
	}
}

type processResourceReading struct {
	memoryBytes uint64
	cpuTimeMS   uint64
}

var errProcessMetricUnavailable = errors.New("process metrics unavailable")

func (r processResourceReading) String() string {
	return fmt.Sprintf("mem=%d cpu_ms=%d", r.memoryBytes, r.cpuTimeMS)
}
