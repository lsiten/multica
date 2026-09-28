package daemon

import (
	"context"
	"strings"
)

// A terminal issue authorizes retention policy, not loss of unpublished Git work.
func (d *Daemon) cleanTaskDir(taskDir string) (int64, bool) {
	ctx := d.recoveryContext()
	repositories, reason := inspectWorktreeRepositories(ctx, taskDir, d.cfg.WorkspacesRoot)
	if reason != "" {
		d.logger.Debug("gc: preserving task repository", "path", taskDir, "reason", reason)
		return 0, false
	}
	if reason := worktreeDeliverablesReason(ctx, taskDir, repositories); reason != "" {
		return 0, false
	}
	return d.removeTaskDir(taskDir)
}

// Other disposable agent branches cannot witness preservation: pruning all of
// them in one cycle would otherwise erase their shared, unpublished history.
func agentBranchPreserved(ctx context.Context, repo, branch string) (bool, error) {
	head, err := worktreeGit(ctx, repo, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		return false, err
	}
	refs, err := worktreeGit(ctx, repo, "for-each-ref", "--contains="+head, "--format=%(refname)", "refs/heads/", "refs/remotes/", "refs/tags/")
	if err != nil {
		return false, err
	}
	for _, ref := range strings.Split(refs, "\n") {
		if ref != "" && !strings.HasPrefix(ref, "refs/heads/agent/") {
			return true, nil
		}
	}
	return false, nil
}

// Removing the cache also removes its non-agent branches and tags. Local refs
// alone cannot prove those objects exist after the whole repository is deleted.
func repoCachePublished(ctx context.Context, repo string) (bool, error) {
	unpublished, err := worktreeGit(ctx, repo, "rev-list", "--all", "--not", "--remotes")
	return err == nil && unpublished == "", err
}
