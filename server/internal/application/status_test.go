package application

import (
	"testing"
	"time"
)

func TestApplicationStatusKeepsObservationAndConnectivitySeparate(t *testing.T) {
	now := time.Now()
	old := now.Add(-5 * time.Minute)
	cases := []struct {
		name, desired, process, health, runtime, want string
		generation, observed                          int64
		at, last                                      *time.Time
	}{
		{"ready", "running", "running", "healthy", "online", "running", 1, 1, &now, &now},
		{"unhealthy", "running", "running", "unhealthy", "online", "unhealthy", 1, 1, &now, &now},
		{"new revision queued", "running", "running", "healthy", "online", "starting", 2, 1, &now, &now},
		{"stop queued", "stopped", "running", "healthy", "online", "stopping", 2, 1, &now, &now},
		{"lost runtime", "running", "running", "healthy", "offline", "offline", 1, 1, &now, &now},
		{"stale heartbeat", "running", "running", "healthy", "online", "offline", 1, 1, &now, &old},
		{"stale service fact", "running", "running", "healthy", "online", "unknown", 1, 1, &old, &now},
		{"confirmed exit", "stopped", "stopped", "unknown", "online", "stopped", 2, 2, &now, &now},
		{"future process enum", "running", "future", "healthy", "online", "unknown", 1, 1, &now, &now},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := InstanceStatus(tc.desired, tc.process, tc.health, tc.runtime, tc.generation, tc.observed, tc.at, tc.last, now); got != tc.want {
				t.Fatalf("status=%s want=%s", got, tc.want)
			}
		})
	}
}
