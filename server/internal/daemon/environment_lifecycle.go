package daemon

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var errEnvironmentTaskScopeChanged = errors.New("latest task does not belong to this environment")

func (d *Daemon) recordEnvironmentCompletion(task Task, result TaskResult, logger *slog.Logger) string {
	if result.EnvRoot == "" {
		return ""
	}
	meta, ok, err := d.gcMetaForTaskRoot(task, result.EnvRoot)
	if err != nil {
		logger.Warn("completion environment owner unavailable; retained for inspection", "error", err)
		return ""
	}
	if !ok {
		return ""
	}
	meta.AutoCleanup = result.Status == "completed"
	if assignment, _ := localDirectoryAssignmentForTask(task, d.cfg.DaemonID); assignment != nil && !assignment.UsesWorktree() {
		meta.LocalDirectory = true
	}
	if err := execenv.WriteGCMeta(result.EnvRoot, meta, logger); err != nil {
		logger.Warn("write gc meta failed; retained for inspection", "error", err)
		return ""
	}
	if result.CodeRoot != "" && result.CodeRoot != result.EnvRoot {
		codeMeta, ok, err := d.gcMetaForTaskRoot(task, result.CodeRoot)
		if err != nil || !ok {
			logger.Warn("shared code completion metadata unavailable", "error", err)
		} else if err := execenv.WriteGCMeta(result.CodeRoot, codeMeta, logger); err != nil {
			logger.Warn("shared code completion metadata could not be saved", "error", err)
		}
	}
	if meta.AutoCleanup || d.cfg.EnvironmentRecycleEnabled {
		return result.EnvRoot
	}
	return ""
}

// The physical root keeps its original owner; a later task can use its code
// while having its own queue entry and review binding.
func (d *Daemon) gcMetaForTaskRoot(task Task, path string) (execenv.GCMeta, bool, error) {
	meta, ok := gcMetaForTask(task)
	if !ok {
		return meta, false, nil
	}
	owner, err := d.gcTaskDirOwner(path)
	if err != nil {
		return meta, false, err
	}
	if owner.WorkspaceID != task.WorkspaceID {
		return meta, false, errors.New("completion environment workspace changed")
	}
	if owner.TaskID != task.ID {
		meta.LatestTaskID = task.ID
		meta.TaskID = owner.TaskID
	}
	return meta, true, nil
}

func (d *Daemon) environmentTaskGCStatus(ctx context.Context, path string, owner *execenv.EnvRootOwner, meta *execenv.GCMeta) (*protocol.TaskGCStatus, error) {
	if d.client == nil || owner == nil {
		return nil, errors.New("environment lifecycle unavailable")
	}
	if meta == nil {
		meta, _ = execenv.ReadGCMeta(path)
	}
	latest := owner.TaskID
	if meta != nil {
		if meta.LatestTaskID != "" {
			latest = meta.LatestTaskID
		} else if meta.TaskID != "" && meta.TaskID != owner.TaskID {
			// Existing reused roots recorded the last task as their physical
			// owner. Authorize that pointer against the server before using it.
			latest = meta.TaskID
		}
	}
	status, err := d.environmentTaskStatus(ctx, latest)
	if isAccessNotFound(err) {
		statuses, batchErr := d.client.GetTaskGCChecks(ctx, owner.WorkspaceID, "", []string{latest})
		if batchErr != nil {
			return nil, batchErr
		}
		status = statuses[latest]
		if status == nil {
			return nil, errEnvironmentTaskScopeChanged
		}
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if status.Missing {
		return status, nil
	}
	if latest != owner.TaskID {
		if meta.WorkspaceID != owner.WorkspaceID || status.WorkspaceID != owner.WorkspaceID ||
			meta.AgentID == "" || status.AgentID != meta.AgentID ||
			!environmentContainsWorkdir(path, status.WorkDir) {
			return nil, errEnvironmentTaskScopeChanged
		}
		if meta.RuntimeID == "" {
			if binding, bindingErr := execenv.ReadReviewRuntime(path); bindingErr == nil && binding.WorkspaceID == owner.WorkspaceID && binding.TaskID == owner.TaskID && binding.RuntimeID == status.RuntimeID && binding.AgentID == status.AgentID {
				return status, nil
			}
			original, originalErr := d.environmentTaskStatus(ctx, owner.TaskID)
			if originalErr != nil || original.WorkspaceID != owner.WorkspaceID || original.RuntimeID == "" || original.RuntimeID != status.RuntimeID || original.AgentID != status.AgentID || original.IssueID != status.IssueID || original.ChatSessionID != status.ChatSessionID {
				return nil, errEnvironmentTaskScopeChanged
			}
		} else if status.RuntimeID != meta.RuntimeID {
			return nil, errEnvironmentTaskScopeChanged
		}
	}
	return status, nil
}

// A restored root need not exist yet. Resolve its existing parent to compare
// a server-recorded canonical path with aliases such as /var on macOS.
func environmentContainsWorkdir(root, workdir string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(workdir) {
		return false
	}
	roots := []string{filepath.Clean(root)}
	if parent, err := util.ResolveSymlinks(filepath.Dir(root)); err == nil {
		roots = append(roots, filepath.Join(parent, filepath.Base(root)))
	}
	for _, base := range roots {
		rel, err := filepath.Rel(base, filepath.Clean(workdir))
		if err != nil || !filepath.IsLocal(rel) || rel == "." {
			continue
		}
		first := strings.Split(rel, string(filepath.Separator))[0]
		if first == "workdir" || first == "worktree" {
			return true
		}
	}
	return false
}
