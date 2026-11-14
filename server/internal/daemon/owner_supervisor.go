package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

type ownerProbe func(context.Context) error

// superviseOwners is the runtime liveness boundary for the required child
// owners. A Daemon with a dead capability owner must stop claiming work and
// exit so the desktop/CLI supervisor can restart a clean topology; continuing
// to report running would turn a child crash into silent task loss.
func (d *Daemon) superviseOwners(ctx context.Context, probes map[string]ownerProbe) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for name, probe := range probes {
			probeCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
			err := probe(probeCtx)
			cancel()
			if err == nil {
				continue
			}
			d.ready.Store(false)
			d.ownerReady.Store(false)
			d.logger.Error("required process owner lost; shutting down daemon", "owner", name, "error", err)
			if d.cancelFunc != nil {
				d.cancelFunc()
			}
			return
		}
	}
}

func probeRuntimeOwner(ctx context.Context, process *runtimeproc.Process) error {
	if process == nil || process.Client == nil {
		return fmt.Errorf("owner process is unavailable")
	}
	status, err := process.Client.Health(ctx)
	if err != nil {
		return err
	}
	if status.State != "ready" && status.State != "running" {
		return fmt.Errorf("owner state is %s", status.State)
	}
	return nil
}
