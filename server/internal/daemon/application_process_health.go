package daemon

import (
	"context"
	"time"
)

// ApplicationServiceHealth summarizes observed child inventory without exposing
// source paths, process credentials, or replacing the existing task counters.
type ApplicationServiceHealth struct {
	State      string `json:"state"`
	InstanceID string `json:"instance_id,omitempty"`
	Running    int    `json:"running"`
	Stopped    int    `json:"stopped"`
	Unknown    bool   `json:"unknown"`
}

func (d *Daemon) applicationProcessHealth(ctx context.Context) *ApplicationServiceHealth {
	if !d.applicationProcessMode() {
		return nil
	}
	result := &ApplicationServiceHealth{State: "unknown", Unknown: true}
	d.applicationProcessMu.Lock()
	client := d.applicationProcess
	d.applicationProcessMu.Unlock()
	if client == nil {
		return result
	}
	result.InstanceID = client.bootstrap.Identity.InstanceID
	query, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	inventory, err := client.inventory(query)
	if err != nil {
		return result
	}
	client.mu.Lock()
	failed := client.syncFailed
	client.mu.Unlock()
	result.State = "running"
	result.Unknown = inventory.Unknown || failed
	if failed {
		result.State = "unknown"
	}
	for _, observation := range inventory.Observations {
		switch observation.ProcessState {
		case "running":
			result.Running++
		case "stopped":
			result.Stopped++
		default:
			result.Unknown = true
		}
	}
	return result
}
