package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func (d *Daemon) sharedWorktreeReportNamespace() string {
	if d.terminalReports == nil {
		return ""
	}
	return d.terminalReports.dir
}

func (d *Daemon) replayPendingWorktreeDeliveries(parent context.Context) (pending, delivered int) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	receipts, err := execenv.PendingSharedWorktreeDeliveries(ctx, d.sharedWorktreeReportNamespace())
	if err != nil {
		d.logger.Warn("read shared worktree delivery receipts", "error", err)
		return 1, 0
	}
	for _, receipt := range receipts {
		body := map[string]any{"branch_name": receipt.Branch, "worktree_commit": receipt.Commit, "work_dir": receipt.WorkDir, "no_work": receipt.NoWork}
		if err := d.client.postJSONWithRetry(ctx, fmt.Sprintf("/api/daemon/tasks/%s/worktree-delivery", receipt.TaskID), body, nil, skillBundleResolveRetrySchedule); err != nil {
			pending++
			d.logger.Warn("shared worktree delivery remains queued", "task", receipt.TaskID, "error", err)
			continue
		}
		if err := execenv.AcknowledgeSharedWorktreeDelivery(ctx, receipt); err != nil {
			pending++
			d.logger.Warn("acknowledge shared worktree delivery", "task", receipt.TaskID, "error", err)
		} else {
			delivered++
		}
	}
	return pending, delivered
}
