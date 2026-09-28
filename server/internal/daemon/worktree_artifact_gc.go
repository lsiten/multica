package daemon

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// Called under the same root reservation, process lock, and read-lease guard as
// full GC. Failed status lookups never become permission to reclaim artifacts.
func (d *Daemon) reclaimRetainedGCArtifacts(ctx context.Context, root string, stats *gcStats) {
	owner, err := d.gcTaskDirOwner(root)
	if err != nil || d.client == nil || d.cfg.GCArtifactTTL <= 0 {
		return
	}
	meta, err := execenv.ReadGCMeta(root)
	if err != nil || meta.CompletedAt.IsZero() || time.Since(meta.CompletedAt) <= d.cfg.GCArtifactTTL {
		return
	}
	status, err := d.client.GetTaskGCCheck(ctx, owner.TaskID)
	if err != nil || !isAgentTaskTerminal(status.Status) {
		return
	}
	if !status.CompletedAt.IsZero() && time.Since(status.CompletedAt) <= d.cfg.GCArtifactTTL {
		return
	}
	d.cleanRetainedWorktreeArtifacts(ctx, root, stats)
}

// Retained deliveries only release known daemon caches or ignored build output
// inside a repository. A familiar basename alone is not ownership evidence.
func (d *Daemon) cleanRetainedWorktreeArtifacts(ctx context.Context, root string, stats *gcStats) {
	removed, bytes, patterns := d.cleanManagedTaskArtifacts(root)
	recordArtifactCleanup(stats, removed, bytes, patterns)
	repositories, reason := inspectWorktreeRepositories(ctx, root, d.cfg.WorkspacesRoot)
	if reason == "unavailable" {
		return
	}
	matcher := newArtifactMatcher(d.cfg.GCArtifactPatterns, nil)
	for _, repository := range repositories {
		err := filepath.WalkDir(repository, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Type()&linkedDirModes != 0 || entry.Name() == ".git" {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !entry.IsDir() || path == repository {
				return nil
			}
			pattern, matched := matcher.matchDirectory(root, path, entry)
			if !matched {
				return nil
			}
			relative, err := filepath.Rel(repository, path)
			if err != nil || !filepath.IsLocal(relative) {
				return fs.SkipDir
			}
			preserved, err := worktreeGit(ctx, repository, "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", filepath.ToSlash(relative))
			if err != nil || preserved != "" {
				return fs.SkipDir
			}
			if artifactContainsRepository(path) {
				return fs.SkipDir
			}
			size := dirSize(path)
			if err := os.RemoveAll(path); err != nil {
				d.logger.Warn("gc: ignored artifact removal failed", "path", path, "error", err)
				return fs.SkipDir
			}
			recordArtifactCleanup(stats, 1, size, map[string]int{pattern: 1})
			return fs.SkipDir
		})
		if err != nil {
			d.logger.Debug("gc: retained artifact scan interrupted", "path", repository, "error", err)
		}
	}
}
