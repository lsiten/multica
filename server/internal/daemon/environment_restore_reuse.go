package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/util"
)

func managedReuseScopeMatches(task Task, scope *execenv.ManagedEnvProvenance) bool {
	if scope == nil || scope.ManagedBy != execenv.ManagedEnvProvenanceManagedBy || scope.WorkspaceID != task.WorkspaceID || scope.AgentID != task.AgentID ||
		scope.RuntimeID != task.RuntimeID || scope.ProjectID != task.ProjectID || scope.SquadID != task.SquadID {
		return false
	}
	fingerprint, err := repositoryScopeForTask(task)
	if err != nil || scope.RepositoryScope != fingerprint {
		return false
	}
	if task.ChatSessionID != "" {
		return scope.ChatSessionID == task.ChatSessionID && scope.IssueID == task.IssueID
	}
	if task.IssueID != "" {
		return scope.IssueID == task.IssueID && scope.ChatSessionID == ""
	}
	return task.AutopilotID != "" && scope.AutopilotID == task.AutopilotID && scope.IssueID == "" && scope.ChatSessionID == ""
}

// Restore only the recorded prior code directory. A corrupted matching backup
// ends preparation instead of silently dropping the work and starting clean.
func (d *Daemon) restoreArchivedPriorWorkdir(ctx context.Context, task Task, local *localDirectoryAssignment) error {
	if local != nil && !local.UsesWorktree() || task.PriorWorkDir == "" || !filepath.IsAbs(task.PriorWorkDir) {
		return nil
	}
	targetRoot := d.priorMissingCodeRoot(task.PriorWorkDir)
	if targetRoot == "" {
		return nil
	}
	if _, err := os.Lstat(task.PriorWorkDir); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Lstat(targetRoot); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	root, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	archiveRoot, err := root.OpenRoot(".environment-archive")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer archiveRoot.Close()
	entries, err := fs.ReadDir(archiveRoot.FS(), ".")
	if err != nil {
		return err
	}
	candidates := []execenv.EnvironmentArchive{}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !entry.IsDir() || entry.Type()&linkedDirModes != 0 {
			continue
		}
		manifest, err := execenv.ReadEnvironmentArchive(d.cfg.WorkspacesRoot, entry.Name())
		if err != nil {
			continue
		}
		path := filepath.Join(d.cfg.WorkspacesRoot, filepath.FromSlash(manifest.RelativeRoot))
		if manifest.Profile == d.cfg.Profile && manifest.BackendURL == d.cfg.ServerBaseURL && manifest.Owner.WorkspaceID == task.WorkspaceID &&
			managedReuseScopeMatches(task, manifest.ReuseScope) && environmentContainsWorkdir(path, task.PriorWorkDir) {
			candidates = append(candidates, manifest)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CreatedAt.After(candidates[j].CreatedAt) })
	if len(candidates) == 0 {
		return nil
	}
	manifest := candidates[0]
	path := filepath.Join(d.cfg.WorkspacesRoot, filepath.FromSlash(manifest.RelativeRoot))
	status, err := d.environmentTaskGCStatus(ctx, path, &manifest.Owner, manifest.Metadata)
	if err != nil {
		return fmt.Errorf("authorize archived workline: %w", err)
	}
	if status.WorkspaceID != task.WorkspaceID || status.RuntimeID != task.RuntimeID || status.AgentID != task.AgentID || !isAgentTaskTerminal(status.Status) {
		return errors.New("archived workline ownership changed")
	}
	if _, err := execenv.RestoreEnvironmentArchive(ctx, d.cfg.WorkspacesRoot, manifest.ID, d.cfg.Profile, d.cfg.ServerBaseURL); err != nil {
		// Another continuation may publish first. It still has to pass the
		// normal provenance and exclusive-writer checks below.
		if _, statErr := os.Lstat(task.PriorWorkDir); statErr == nil {
			return nil
		}
		return fmt.Errorf("restore archived workline %s: %w", manifest.ID, err)
	}
	d.logger.Info("archived workline restored for continuation", "task_id", task.ID, "archive_id", manifest.ID, "root", filepath.Base(path))
	return nil
}

func (d *Daemon) priorMissingCodeRoot(workdir string) string {
	bases := []string{filepath.Clean(d.cfg.WorkspacesRoot)}
	if resolved, err := util.ResolveSymlinks(d.cfg.WorkspacesRoot); err == nil {
		bases = append(bases, resolved)
	}
	for _, base := range bases {
		rel, err := filepath.Rel(base, filepath.Clean(workdir))
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) >= 3 && (parts[2] == "workdir" || parts[2] == "worktree") {
			return filepath.Join(base, parts[0], parts[1])
		}
	}
	return ""
}
