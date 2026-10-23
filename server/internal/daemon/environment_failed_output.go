package daemon

import (
	"context"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// A failed run cannot certify delivery. Automatic deletion needs a clean
// initial baseline or independent evidence that Git history is preserved.
func (d *Daemon) failedEnvironmentOutputReason(ctx context.Context, path string, status *protocol.TaskGCStatus) string {
	scope, scoped := ctx.Value(environmentScopeContextKey{}).(environmentOperationScope)
	if !scoped || !scope.Automatic || (status.Status != "failed" && status.Status != "cancelled") {
		return ""
	}
	repositories, reason := inspectWorktreeRepositories(ctx, path, d.cfg.WorkspacesRoot)
	if reason != "" {
		return reason
	}
	if reason := worktreeDeliverablesReason(ctx, path, repositories); reason != "" {
		return reason
	}
	baseline, baselineErr := execenv.ReadRetainedLocalWorktree(path)
	for _, repository := range repositories {
		head, err := worktreeGit(ctx, repository, "rev-parse", "--verify", "HEAD")
		if err != nil {
			return "unavailable"
		}
		if baselineErr == nil && !baseline.Continued && filepath.Clean(repository) == filepath.Clean(baseline.Path) && baseline.BaseCommit != "" && head == baseline.BaseCommit {
			continue
		}
		branch, err := worktreeGit(ctx, repository, "symbolic-ref", "--quiet", "--short", "HEAD")
		if err != nil {
			return "unavailable"
		}
		preserved, err := agentBranchPreserved(ctx, repository, branch)
		if err != nil {
			return "unavailable"
		}
		if !preserved {
			return "unpushed"
		}
	}
	return ""
}
