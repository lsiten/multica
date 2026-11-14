//go:build windows

package daemon

import (
	"os"
	"testing"
)

func TestWindowsProcessMetricsSamplesCurrentProcess(t *testing.T) {
	metrics := sampleProcessMetrics(os.Getpid())
	if !metrics.Available {
		t.Fatalf("current process metrics unavailable: %+v", metrics)
	}
	if metrics.PID != os.Getpid() || metrics.MemoryBytes == 0 {
		t.Fatalf("unexpected current process metrics: %+v", metrics)
	}
	tree := sampleProcessTree(os.Getpid())
	if !tree.Available || tree.ProcessCount < 1 {
		t.Fatalf("current process tree unavailable: %+v", tree)
	}
}
