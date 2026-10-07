package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// A run's configuration directory does not inherit the lifetime of the shared
// code it references. Only an actual code root can be pinned by a business task.
func environmentHasCode(path string) (bool, error) {
	for _, name := range []string{"workdir", "worktree"} {
		entry, err := os.Lstat(filepath.Join(path, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if entry.IsDir() && entry.Mode()&linkedDirModes == 0 {
			return true, nil
		}
		return false, errors.New("code root is not an owned directory")
	}
	return false, nil
}

func (d *Daemon) environmentRetentionReason(ctx context.Context, path string, status *protocol.TaskGCStatus) string {
	if referenced, err := d.applicationReferencesDirectory(path); err != nil {
		return "unavailable"
	} else if referenced {
		return "application"
	}
	if d.isActiveEnvRoot(path) {
		return "active"
	}
	if !status.LifecycleSupported || !status.RetentionSupported {
		return "unavailable"
	}
	if !isAgentTaskTerminal(status.Status) && !status.Missing {
		return "active"
	}
	if binding, err := execenv.ReadWorktreeBinding(path); err == nil {
		for _, consumer := range binding.Consumers {
			current, err := d.environmentTaskStatus(ctx, consumer.TaskID)
			if isAccessNotFound(err) {
				statuses, batchErr := d.client.GetTaskGCChecks(ctx, binding.Owner.WorkspaceID, consumer.RuntimeID, []string{consumer.TaskID})
				current, err = statuses[consumer.TaskID], batchErr
				if current == nil {
					return "unavailable"
				}
			}
			if err != nil || current.WorkspaceID != binding.Owner.WorkspaceID {
				return "unavailable"
			}
			if current.Missing {
				continue
			}
			if current.RuntimeID != consumer.RuntimeID || current.AgentID != consumer.AgentID {
				return "unavailable"
			}
			if !isAgentTaskTerminal(current.Status) {
				return "active"
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "unavailable"
	}
	if references, ok := ctx.Value(environmentReviewReferencesKey{}).(map[string]environmentReviewReference); ok && references != nil {
		if _, pinned := references[executionEnvClaimKey(path)]; pinned {
			return "task_review"
		}
	} else if roots, err := d.environmentRootPaths(ctx); err == nil {
		reviewContext := d.withEnvironmentReviewReferences(ctx, roots)
		references, _ := reviewContext.Value(environmentReviewReferencesKey{}).(map[string]environmentReviewReference)
		if _, pinned := references[executionEnvClaimKey(path)]; pinned {
			return "task_review"
		}
	}
	if status.Missing {
		return ""
	}

	code, err := environmentHasCode(path)
	if err != nil {
		return "unavailable"
	}
	if !code {
		return ""
	}

	if !environmentContainsWorkdir(path, status.CurrentWorkDir) {
		return ""
	}
	if status.IssueID != "" {
		if status.IssueStatusCategory == issuestatus.CategoryDone || status.IssueStatusCategory == issuestatus.CategoryClosed {
			return ""
		}
		if status.IssueStatus != "" && !issuestatus.IsCategory(status.IssueStatusCategory) {
			return "unavailable"
		}
	}
	if status.WaitingHuman {
		return "waiting_human"
	}
	if status.IssueStatusCategory == issuestatus.CategoryStarted && status.CurrentAgent {
		switch status.IssueStatus {
		case issuestatus.InReview:
			return "task_review"
		case issuestatus.Blocked:
			return "task_blocked"
		default:
			return "task_in_progress"
		}
	}
	return ""
}
