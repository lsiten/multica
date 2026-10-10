package daemon

import (
	"context"
	"encoding/json"
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
	namespace := d.sharedWorktreeReportNamespace()

	var receipts []execenv.SharedWorktreeDelivery
	var err error
	if d.environmentProcessMode() {
		receipts, err = d.childWorktreeDeliveries(ctx, namespace)
	} else {
		receipts, err = execenv.PendingSharedWorktreeDeliveries(ctx, namespace)
	}
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
		var ackErr error
		if d.environmentProcessMode() {
			ackErr = d.ackChildWorktreeDelivery(ctx, receipt)
		} else {
			ackErr = execenv.AcknowledgeSharedWorktreeDelivery(ctx, receipt)
		}
		if ackErr != nil {
			pending++
			d.logger.Warn("acknowledge shared worktree delivery", "task", receipt.TaskID, "error", ackErr)
		} else {
			delivered++
		}
	}
	return pending, delivered
}

// childWorktreeDeliveries reads settled shared-worktree delivery receipts from
// the environment service, which owns the shared directory cache state. The
// server report (and its auth) stays in the parent; only the receipt read and
// retirement move to the child so a single owner owns the shared directory.
func (d *Daemon) childWorktreeDeliveries(ctx context.Context, namespace string) ([]execenv.SharedWorktreeDelivery, error) {
	child, err := d.ensureEnvironmentProcess(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := child.process.Client.Read(ctx, "worktree-delivery.receipts", marshalRaw(struct {
		Namespace string `json:"namespace"`
	}{namespace}))
	if err != nil {
		return nil, err
	}
	var receipts []execenv.SharedWorktreeDelivery
	if err := json.Unmarshal(raw, &receipts); err != nil {
		return nil, err
	}
	return receipts, nil
}

// ackChildWorktreeDelivery retires a server-accepted receipt through the
// environment service so the exact-generation comparison runs against the
// cache state the child owns.
func (d *Daemon) ackChildWorktreeDelivery(ctx context.Context, receipt execenv.SharedWorktreeDelivery) error {
	child, err := d.ensureEnvironmentProcess(ctx)
	if err != nil {
		return err
	}
	return child.mutation(ctx, "worktree-delivery.ack", receipt, nil)
}
