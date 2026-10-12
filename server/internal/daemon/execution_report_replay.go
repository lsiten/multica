package daemon

import (
	"context"
	"fmt"
)

func (d *Daemon) sendRecoveredExecutionReport(ctx context.Context, report terminalTaskReport) error {
	operation, err := terminalReportKindName(report.kind)
	if err != nil {
		return err
	}
	payload := map[string]any{}
	switch report.kind {
	case taskMessagesReport:
		payload["messages"] = report.messages
	case taskUsageReport:
		payload["usage"] = report.usage
	case terminalTaskReportComplete:
		payload["output"] = report.output
		payload["worktree_delivery_pending"] = report.worktreeDeliveryPending
		if report.worktreeCommit != "" {
			payload["worktree_commit"] = report.worktreeCommit
		}
	case terminalTaskReportFail:
		payload["error"] = report.errorMessage
		if report.failureReason != "" {
			payload["failure_reason"] = report.failureReason
		}
	}
	if report.branchName != "" {
		payload["branch_name"] = report.branchName
	}
	if report.sessionID != "" {
		payload["session_id"] = report.sessionID
	}
	if report.workDir != "" {
		payload["work_dir"] = report.workDir
	}
	if report.durableWorkDir != "" {
		payload["durable_work_dir"] = report.durableWorkDir
	}
	if report.retiredSessionID != "" {
		payload["retired_session_id"] = report.retiredSessionID
	}
	if report.sessionRolloutMissing {
		payload["session_rollout_missing"] = true
	}
	if report.jevVerification != nil {
		payload["jev_verification"] = report.jevVerification
	}
	path := fmt.Sprintf("/api/daemon/runtimes/%s/executions/reports", report.execution.RuntimeID)
	return d.client.postJSON(ctx, path, map[string]any{"identity": report.execution, "operation": operation, "payload": payload}, nil)
}
