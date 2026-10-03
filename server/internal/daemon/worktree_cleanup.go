package daemon

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
)

// cleanupManagedWorktree shares exclusion and preservation rules between
// automatic cleanup and a user-confirmed cleanup request.
type worktreeCleanup struct {
	path           string
	automatic      bool
	discardChanges bool
	artifactStats  *gcStats
}

func (d *Daemon) cleanupManagedWorktree(ctx context.Context, request worktreeCleanup) string {
	path := request.path
	release, available := d.reserveEnvRootForGC(path)
	if !available {
		return "active"
	}
	defer release()
	owner, err := d.gcTaskDirOwner(path)
	if err != nil {
		return "unowned"
	}
	root, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		return "unavailable"
	}
	defer root.Close()
	rel, err := filepath.Rel(d.cfg.WorkspacesRoot, path)
	if err != nil {
		return "unowned"
	}
	claim, info, err := execenv.LockEnvRootForReuse(root, rel, path)
	if errors.Is(err, execenv.ErrEnvRootBusy) {
		return "active"
	}
	if err != nil || claim == nil {
		return "unavailable"
	}
	defer claim.Release()
	current, err := root.Lstat(rel)
	if err != nil || !os.SameFile(info, current) || current.Mode()&linkedDirModes != 0 {
		return "unowned"
	}
	if d.client == nil {
		return "unavailable"
	}
	if active, err := localreview.HasActiveReview(ctx, path, time.Now()); err != nil {
		return "unavailable"
	} else if active {
		return "review"
	}
	status, err := d.environmentTaskGCStatus(ctx, path, owner, nil)
	if err != nil || !isAgentTaskTerminal(status.Status) {
		return "unavailable"
	}
	if !validScopedTaskStatus(ctx, owner, status) {
		return "scope_changed"
	}
	if request.automatic {
		meta, err := execenv.ReadGCMeta(path)
		if err != nil || !meta.AutoCleanup || meta.LocalDirectory {
			return "retained"
		}
		if request.artifactStats != nil {
			completed := meta.CompletedAt
			if status.CompletedAt.After(completed) {
				completed = status.CompletedAt
			}
			if d.cfg.GCArtifactTTL <= 0 || completed.IsZero() || time.Since(completed) <= d.cfg.GCArtifactTTL {
				return "retained"
			}
			d.cleanRetainedWorktreeArtifacts(ctx, path, request.artifactStats)
			return ""
		}
		if status.Status != "completed" {
			return "retained"
		}
		// Output may be the sole copy of a generated deliverable. Only explicit
		// manual cleanup may remove it together with the task's logs.
		if outputContainsFiles(filepath.Join(path, "output")) {
			return "output"
		}
	}
	repositories, reason := inspectWorktreeRepositories(ctx, path, d.cfg.WorkspacesRoot)
	if request.discardChanges && !request.automatic && (reason == "dirty" || reason == "unpushed") {
		reason = ""
	}
	if reason != "" {
		return reason
	}
	if request.automatic || !request.discardChanges {
		if reason := worktreeDeliverablesReason(ctx, path, repositories); reason != "" {
			return reason
		}
	}
	if err := execenv.ArchiveReviewDirectory(ctx, d.cfg.WorkspacesRoot, path); err != nil {
		return "unavailable"
	}
	for _, repo := range repositories {
		common, err := worktreeGit(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return "unavailable"
		}
		gitDir, err := worktreeGit(ctx, repo, "rev-parse", "--absolute-git-dir")
		if err != nil {
			return "unavailable"
		}
		if common != gitDir {
			args := []string{"worktree", "remove"}
			if request.discardChanges && !request.automatic {
				args = append(args, "--force")
			}
			if _, err := worktreeGit(ctx, common, append(args, repo)...); err != nil {
				return "unavailable"
			}
		}
	}
	if _, removed := d.removeTaskDir(path); !removed {
		return "unavailable"
	}
	return ""
}

func worktreeGit(ctx context.Context, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	options := []string{"-c", "core.fsmonitor=false", "-C", path}
	if len(args) > 0 && (args[0] == "status" || args[0] == "diff") {
		filters, err := localreview.InspectionFilterOptions(ctx, path)
		if err != nil {
			return "", err
		}
		options = append(options, filters...)
	}
	cmd := exec.CommandContext(ctx, "git", append(options, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	output, err := cmd.Output()
	return strings.TrimSpace(string(output)), err
}

// Files beside a checkout may be the only copy of generated deliverables.
func hasNonRepositoryFiles(root string, repositories []string) bool {
	repos := make(map[string]bool, len(repositories))
	for _, repo := range repositories {
		repos[repo] = true
	}
	for _, name := range []string{"workdir", "worktree"} {
		found := false
		err := filepath.WalkDir(filepath.Join(root, name), func(path string, entry fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) && path == filepath.Join(root, name) {
				return nil
			}
			if err != nil {
				return err
			}
			if repos[path] && entry.IsDir() {
				return fs.SkipDir
			}
			if !entry.IsDir() {
				found = true
			}
			return nil
		})
		if err != nil || found {
			return true
		}
	}
	return false
}

// A clean linked branch survives worktree removal in its common repository.
// Standalone clones and detached HEADs must have their commits on a remote ref.
func inspectWorktreeRepositories(ctx context.Context, root, workspacesRoot string) ([]string, string) {
	repositories := []string{}
	for _, name := range []string{"workdir", "worktree"} {
		path := filepath.Join(root, name)
		err := filepath.WalkDir(path, func(path string, entry fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) && path == filepath.Join(root, name) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.Type()&linkedDirModes != 0 {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !entry.IsDir() {
				return nil
			}
			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				repositories = append(repositories, path)
				return fs.SkipDir
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		})
		if err != nil {
			return nil, "unavailable"
		}
	}
	for _, repo := range repositories {
		status, err := worktreeGit(ctx, repo, "status", "--porcelain", "--untracked-files=all")
		if err != nil {
			return nil, "unavailable"
		}
		if status != "" {
			return repositories, "dirty"
		}
		stash, err := worktreeGit(ctx, repo, "stash", "list")
		if err != nil {
			return repositories, "unavailable"
		}
		if stash != "" {
			return repositories, "dirty"
		}
		common, err := worktreeGit(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return nil, "unavailable"
		}
		gitDir, err := worktreeGit(ctx, repo, "rev-parse", "--absolute-git-dir")
		if err != nil {
			return nil, "unavailable"
		}
		branch, err := worktreeGit(ctx, repo, "symbolic-ref", "--quiet", "HEAD")
		canonicalRoot, rootErr := filepath.EvalSymlinks(workspacesRoot)
		canonicalCommon, commonErr := filepath.EvalSymlinks(common)
		if rootErr != nil || commonErr != nil {
			return repositories, "unavailable"
		}
		rel, relErr := filepath.Rel(canonicalRoot, canonicalCommon)
		if common != gitDir && err == nil && branch != "" && relErr == nil && !filepath.IsLocal(rel) {
			continue
		}
		unpushed, err := worktreeGit(ctx, repo, "rev-list", "HEAD", "--branches", "--tags", "--not", "--remotes")
		if err != nil {
			return repositories, "unavailable"
		}
		if unpushed != "" {
			return repositories, "unpushed"
		}
	}
	return repositories, ""
}

func (d *Daemon) autoCleanupCompletedWorktree(ctx context.Context, root string) {
	if root == "" || !d.cfg.GCEnabled || d.cfg.KeepEnvAfterTask {
		return
	}
	if d.cfg.EnvironmentArchiveEnabled {
		d.scheduleAutomaticEnvironmentRecycle(d.recoveryContext(), root)
		return
	}
	if reason := d.cleanupManagedWorktree(ctx, worktreeCleanup{path: root, automatic: true}); reason != "" {
		d.logger.Debug("worktree automatic cleanup retained environment", "path", root, "reason", reason)
	}
}
