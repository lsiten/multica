package daemon

import (
	"context"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

type environmentCleanupResult struct {
	EnvironmentID string `json:"environment_id"`
	WorkspaceID   string `json:"workspace_id"`
	TaskID        string `json:"task_id"`
	Revision      string `json:"revision"`
	Reason        string `json:"reason"`
	Reclaimed     bool   `json:"reclaimed"`
	OriginalBytes int64  `json:"original_bytes"`
	RemovedBytes  int64  `json:"removed_bytes"`
}

// Delete only a proven, unused managed environment. Unlike archival this never
// copies code, outputs or logs into a persistent recovery directory.
func (d *Daemon) cleanupUnreferencedEnvironment(ctx context.Context, path, revision string) environmentCleanupResult {
	if revision != "" {
		ctx = freshEnvironmentLifecycle(ctx)
	}
	result := environmentCleanupResult{}
	owner, release, reason := d.lockArchiveEnvironment(ctx, path)
	if reason != "" {
		result.Reason = reason
		return result
	}
	defer release()
	result.WorkspaceID, result.TaskID = owner.WorkspaceID, owner.TaskID
	result.EnvironmentID = d.managedEnvironmentID(path, owner.WorkspaceID, owner.TaskID)
	unlockRepositories, err := execenv.LockEnvironmentArchiveRepositories(ctx, path)
	if err != nil {
		result.Reason = "repository_busy"
		return result
	}
	defer unlockRepositories()
	status, err := d.environmentTaskGCStatus(ctx, path, owner, nil)
	if err != nil {
		result.Reason = "unavailable"
		return result
	}
	if reason := d.environmentRetentionReason(ctx, path, status); reason != "" {
		result.Reason = reason
		return result
	}
	result.OriginalBytes = dirSize(path)
	result.Revision, err = execenv.EnvironmentArchiveRevision(ctx, path)
	if err != nil {
		result.Reason = "unavailable"
		return result
	}
	if revision == "" {
		return result
	}
	if revision != result.Revision {
		result.Reason = "preview_changed"
		return result
	}
	status, err = d.environmentTaskGCStatus(freshEnvironmentLifecycle(ctx), path, owner, nil)
	if err != nil || !validScopedTaskStatus(ctx, owner, status) {
		result.Reason = "scope_changed"
		return result
	}
	if reason := d.environmentRetentionReason(freshEnvironmentLifecycle(ctx), path, status); reason != "" {
		result.Reason = reason
		return result
	}
	if err := execenv.RemoveEnvironmentDirectory(ctx, d.cfg.WorkspacesRoot, path, *owner, revision); err != nil {
		d.logger.Warn("unused environment deletion failed", "task_id", owner.TaskID, "error", err)
		result.Reason = "unavailable"
		return result
	}
	result.Reclaimed = true
	result.RemovedBytes = result.OriginalBytes
	return result
}
