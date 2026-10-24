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

// sampleProcessMetrics returns a best-effort resource reading for a single pid.
// It never fails hard: a process that vanished or a platform without a sampler
// returns an empty reading with Available=false, so the panel degrades to "—".
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

// ProcessTreeMetrics is a point-in-time aggregate of one process and its entire
// descendant tree (children, grandchildren, ...). It is the number a person
// means by "this process and everything it runs": the single-pid reading
// excludes children, so it can be far smaller than what an activity monitor
// shows for the same top-level process. It is local-only diagnostic data and
// carries no secret.
//
// A reading is best-effort: ProcessCount is the number of processes summed and
// is 0 when the root could not be sampled, so a caller can show "—" rather than
// a wrong 0. A process tree has no shared descendants, so no per-pid dedup is
// needed here; callers that union several roots must dedup by pid themselves.
type ProcessTreeMetrics struct {
	ProcessCount int    `json:"process_count,omitempty"`
	MemoryBytes  uint64 `json:"memory_bytes,omitempty"`
	CpuTimeMS    uint64 `json:"cpu_time_ms,omitempty"`
	Available    bool   `json:"available"`
}

// sampleProcessTree returns a best-effort aggregate of the whole descendant tree
// of rootPID: its own reading plus every child, grandchild, ... down the tree.
// It never fails hard: a root that vanished or a platform without a sampler
// returns an empty reading with Available=false.
func sampleProcessTree(rootPID int) ProcessTreeMetrics {
	if rootPID <= 0 {
		return ProcessTreeMetrics{Available: false}
	}
	reading, count, ok := sampleProcessTreeResources(rootPID)
	return ProcessTreeMetrics{
		ProcessCount: count,
		MemoryBytes:  reading.memoryBytes,
		CpuTimeMS:    reading.cpuTimeMS,
		Available:    ok,
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
