package daemon

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// HealthProcessOwner is the authenticated liveness view for one required
// child-process owner. It explains which owner is missing when aggregate
// owners_ready is false, without exposing credentials or private paths.
type HealthProcessOwner struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	State      string `json:"state"`
	Ready      bool   `json:"ready"`
	Reason     string `json:"reason,omitempty"`
}

type processOwnerHealthSpec struct {
	name       string
	configured bool
	process    func() *runtimeproc.Process
	active     func() bool
}

func (d *Daemon) processOwnersHealth(ctx context.Context) []HealthProcessOwner {
	hasOwner := func(name string) bool { return slices.Contains(d.cfg.ProcessServices, name) }
	specs := []processOwnerHealthSpec{
		{name: "ai", configured: hasOwner("ai"), process: d.snapshotAIProcess, active: func() bool { return false }},
		{name: "mirror", configured: hasOwner("mirror"), process: d.snapshotMirrorProcess, active: d.mirrorProcessMode},
		{name: "application", configured: hasOwner("application"), process: d.snapshotApplicationProcess, active: d.applicationProcessMode},
		{name: "environment", configured: hasOwner("environment"), process: d.snapshotEnvironmentProcess, active: d.environmentProcessMode},
		{name: "gateway", configured: hasOwner("gateway"), process: d.snapshotGatewayProcess, active: d.gatewayProcessMode},
	}
	results := make([]HealthProcessOwner, len(specs))
	var wg sync.WaitGroup
	for index, spec := range specs {
		wg.Add(1)
		go func(index int, spec processOwnerHealthSpec) {
			defer wg.Done()
			owner := HealthProcessOwner{Name: spec.name, Configured: spec.configured}
			if !spec.configured {
				owner.State = "disabled"
				results[index] = owner
				return
			}
			process := spec.process()
			if process == nil {
				owner.State = "failed"
				owner.Reason = "required-owner-missing"
				if spec.active() {
					owner.State = "starting"
					owner.Reason = "child-not-ready"
				}
				results[index] = owner
				return
			}
			if process.Client == nil {
				owner.State = "unavailable"
				owner.Reason = "transport-missing"
				results[index] = owner
				return
			}
			probeCtx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
			status, err := process.Client.Health(probeCtx)
			cancel()
			if err != nil {
				owner.State = "unavailable"
				owner.Reason = "health-probe-failed"
				results[index] = owner
				return
			}
			owner.State = status.State
			owner.Ready = status.State == "ready" || status.State == "running"
			results[index] = owner
		}(index, spec)
	}
	wg.Wait()
	return results
}
