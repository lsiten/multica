package daemon

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
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
	if reconciledEnvironmentProvenance(meta, status) && meta.CompletedAt.Equal(status.CompletedAt) {
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
	kind := environmentRunKind(status)
	if meta == nil {
		if kind == "" {
			return nil, errors.New("environment run kind not proven")
		}
		meta = &execenv.GCMeta{TaskID: owner.TaskID, WorkspaceID: owner.WorkspaceID}
	}
	if kind != "" {
		meta.Kind = kind
	}
	if meta.AgentID != status.AgentID {
		meta.AgentName = ""
	}
	meta.WorkspaceID = owner.WorkspaceID
	meta.RuntimeID, meta.AgentID = status.RuntimeID, status.AgentID
	if status.ProjectID != "" || status.RunKind != "" {
		if meta.ProjectID != status.ProjectID {
			meta.ProjectName = ""
		}
		meta.ProjectID = status.ProjectID
	}
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

// Older servers omit the explicit kind but still prove issue/chat/automation
// associations. An issue-less unknown run must never become quick-create.
func environmentRunKind(status *protocol.TaskGCStatus) execenv.GCMetaKind {
	if status.ChatSessionID != "" {
		return execenv.GCKindChat
	}
	if status.IssueID != "" {
		return execenv.GCKindIssue
	}
	if status.AutopilotRunID != "" {
		return execenv.GCKindAutopilotRun
	}
	switch status.RunKind {
	case string(execenv.GCKindChat):
		return execenv.GCKindChat
	case string(execenv.GCKindIssue):
		return execenv.GCKindIssue
	case string(execenv.GCKindAutopilotRun):
		return execenv.GCKindAutopilotRun
	case string(execenv.GCKindQuickCreate):
		return execenv.GCKindQuickCreate
	case string(execenv.GCKindProjectSupervision):
		return execenv.GCKindProjectSupervision
	default:
		return ""
	}
}

func reconciledEnvironmentProvenance(meta *execenv.GCMeta, status *protocol.TaskGCStatus) bool {
	if meta == nil {
		return false
	}
	kind := environmentRunKind(status)
	return (kind == "" || meta.Kind == kind) &&
		meta.WorkspaceID == status.WorkspaceID && meta.RuntimeID == status.RuntimeID && meta.AgentID == status.AgentID &&
		meta.IssueID == status.IssueID && meta.ChatSessionID == status.ChatSessionID &&
		meta.AutopilotRunID == status.AutopilotRunID && meta.AutopilotID == status.AutopilotID &&
		(status.ProjectID == "" && status.RunKind == "" || meta.ProjectID == status.ProjectID) &&
		meta.AutoCleanup == (status.Status == "completed")
}
