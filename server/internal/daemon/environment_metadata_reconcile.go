package daemon

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// Recover missing completion records from authenticated task facts, never age
// or a guessed UUID. Repeated scans leave verified records unchanged.
func (d *Daemon) reconcileEnvironmentMetadata(ctx context.Context, path string) (*execenv.GCMeta, error) {
	owner, err := d.gcTaskDirOwner(path)
	if err != nil {
		return nil, err
	}
	meta, readErr := execenv.ReadGCMeta(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return nil, readErr
	}
	status, err := d.environmentTaskGCStatus(ctx, path, owner, meta)
	if err != nil {
		return nil, err
	}
	if status.Missing {
		return meta, nil
	}
	if !status.RetentionSupported || !status.LifecycleSupported || status.WorkspaceID != owner.WorkspaceID || status.AgentID == "" || status.RuntimeID == "" || !isAgentTaskTerminal(status.Status) || status.CompletedAt.IsZero() {
		return nil, errors.New("environment completion not proven")
	}
	if meta != nil && meta.RuntimeID != "" && meta.AgentID != "" && !meta.CompletedAt.IsZero() {
		return meta, nil
	}
	release, available := d.reserveEnvRootForGC(path)
	if !available {
		return nil, execenv.ErrEnvRootBusy
	}
	defer release()
	unlock, err := d.lockGCTaskDirectory(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	current, err := d.gcTaskDirOwner(path)
	if err != nil || *current != *owner {
		return nil, errors.New("environment ownership changed during reconciliation")
	}
	currentMeta, currentErr := execenv.ReadGCMeta(path)
	if currentErr != nil && !errors.Is(currentErr, os.ErrNotExist) {
		return nil, currentErr
	}
	status, err = d.environmentTaskGCStatus(freshEnvironmentLifecycle(ctx), path, owner, currentMeta)
	if err != nil || !status.RetentionSupported || !status.LifecycleSupported || status.WorkspaceID != owner.WorkspaceID || status.AgentID == "" || status.RuntimeID == "" || !isAgentTaskTerminal(status.Status) || status.CompletedAt.IsZero() {
		return nil, errors.New("environment completion changed during reconciliation")
	}
	meta = currentMeta
	if meta == nil {
		meta = &execenv.GCMeta{TaskID: owner.TaskID, WorkspaceID: owner.WorkspaceID, Kind: execenv.GCKindQuickCreate}
		if status.ChatSessionID != "" {
			meta.Kind = execenv.GCKindChat
		} else if status.IssueID != "" {
			meta.Kind = execenv.GCKindIssue
		} else if status.AutopilotRunID != "" {
			meta.Kind = execenv.GCKindAutopilotRun
		}
	}
	meta.RuntimeID, meta.AgentID = status.RuntimeID, status.AgentID
	meta.IssueID, meta.ChatSessionID = status.IssueID, status.ChatSessionID
	meta.AutopilotRunID, meta.AutopilotID = status.AutopilotRunID, status.AutopilotID
	meta.CompletedAt = status.CompletedAt.UTC()
	meta.AutoCleanup = status.Status == "completed"
	if err := execenv.SaveGCMeta(path, *meta); err != nil {
		return nil, err
	}
	d.logger.Info("environment completion metadata reconciled", "task_id", owner.TaskID, "completed_at", meta.CompletedAt.Format(time.RFC3339))
	return meta, nil
}
