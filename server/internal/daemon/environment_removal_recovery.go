package daemon

import (
	"context"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func (d *Daemon) resumeInterruptedEnvironmentRemovals(ctx context.Context) {
	receipts, err := execenv.PendingEnvironmentRemovals(d.cfg.WorkspacesRoot)
	if err != nil {
		d.logger.Warn("environment deletion receipts unavailable", "error", err)
		return
	}
	for id, receipt := range receipts {
		if ctx.Err() != nil {
			return
		}
		path := filepath.Join(d.cfg.WorkspacesRoot, filepath.FromSlash(receipt.RelativeRoot))
		status, err := d.environmentTaskGCStatus(freshEnvironmentLifecycle(ctx), path, &receipt.Owner, nil)
		if err != nil || !status.RetentionSupported || status.WorkspaceID != receipt.Owner.WorkspaceID || !status.Missing && !isAgentTaskTerminal(status.Status) {
			continue
		}
		release, available := d.reserveEnvRootForGC(path)
		if !available {
			continue
		}
		if err := execenv.FinishEnvironmentRemoval(ctx, d.cfg.WorkspacesRoot, id, receipt); err != nil {
			d.logger.Warn("environment deletion resume failed", "task_id", receipt.Owner.TaskID, "error", err)
		}
		release()
	}
}
