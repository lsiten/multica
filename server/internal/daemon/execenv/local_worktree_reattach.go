package execenv

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ReattachRestoredLocalWorktree replaces only the restored standalone Git
// metadata with a linked registration. Working files and the staged index are
// preserved. Any failed transition rolls back to the self-contained recovery.
func ReattachRestoredLocalWorktree(ctx context.Context, previous *LocalWorktree, params LocalWorktreeParams, logger *slog.Logger) (resultErr error) {
	if !LocalWorktreeReuseMatches(previous, params) {
		return errors.New("restored local worktree scope changed")
	}
	gitInfo, err := os.Lstat(filepath.Join(previous.Path, ".git"))
	if err != nil {
		return err
	}
	if !gitInfo.IsDir() {
		return nil
	}
	unlock, err := lockGitRoot(previous.GitRoot, logger)
	if err != nil {
		return err
	}
	defer unlock()
	record, owned := branchOwnedBy(previous.GitRoot, previous.Branch, params.owner(), logger)
	if !owned {
		return errors.New("restored branch no longer belongs to this workline")
	}
	restoredHead, err := archiveGit(ctx, previous.Path, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	originalHead, err := archiveGit(ctx, previous.GitRoot, "rev-parse", "--verify", "refs/heads/"+previous.Branch)
	if err != nil || originalHead != restoredHead || record.checkpoint == "" {
		return errors.New("original branch changed since archival; recovered code retained")
	}
	root, err := os.OpenRoot(filepath.Dir(previous.Path))
	if err != nil {
		return err
	}
	defer root.Close()
	stageName := ".reattach-" + rand.Text()
	stagePath := filepath.Join(filepath.Dir(previous.Path), stageName)
	if _, err := archiveGit(ctx, previous.GitRoot, "worktree", "add", "--no-checkout", stagePath, previous.Branch); err != nil {
		return fmt.Errorf("attach recovered branch: %w", err)
	}
	registrationManaged := false
	defer func() {
		if !registrationManaged {
			removeLocalWorktreeDir(previous.GitRoot, stagePath, logger)
		}
	}()
	private, err := archiveGit(ctx, stagePath, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	common, err := archiveGit(ctx, previous.GitRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return err
	}
	private, err = filepath.EvalSymlinks(private)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(common, private)
	if err != nil || !filepath.IsLocal(relative) || !strings.HasPrefix(filepath.ToSlash(relative), "worktrees/") {
		return errors.New("new worktree registration escaped common repository")
	}
	admin, err := os.OpenRoot(common)
	if err != nil {
		return err
	}
	defer admin.Close()
	registrationManaged = true
	backup := ".restored-git-" + rand.Text()
	worktreeName := filepath.Base(previous.Path)
	committed, swapped := false, false
	defer func() {
		if !committed {
			if swapped {
				removeErr := root.Remove(filepath.Join(worktreeName, ".git"))
				if errors.Is(removeErr, os.ErrNotExist) {
					removeErr = nil
				}
				restoreErr := root.Rename(backup, filepath.Join(worktreeName, ".git"))
				resultErr = errors.Join(resultErr, removeErr, restoreErr)
			}
			resultErr = errors.Join(resultErr, admin.RemoveAll(relative))
		}
		root.RemoveAll(stageName)
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(filepath.Join(worktreeName, ".git"), backup); err != nil {
		return err
	}
	swapped = true
	if err := root.Rename(filepath.Join(stageName, ".git"), filepath.Join(worktreeName, ".git")); err != nil {
		return err
	}
	recoveryName := ".reattach-metadata-" + rand.Text()
	recoveryPath := filepath.Join(filepath.Dir(previous.Path), recoveryName)
	defer root.RemoveAll(recoveryName)
	backupPath := filepath.Join(filepath.Dir(previous.Path), backup)
	if _, err := captureArchivedGitMetadata(ctx, backupPath, backupPath, recoveryPath, len(restoredHead)); err != nil {
		return err
	}
	if err := restoreArchivedGitMetadata(ctx, recoveryPath, private); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(filepath.Dir(previous.Path), backup, "index"), filepath.Join(private, "index")); err != nil {
		return err
	}
	if _, err := archiveGit(ctx, previous.GitRoot, "worktree", "repair", previous.Path); err != nil {
		return err
	}
	actual, err := archiveGit(ctx, previous.Path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	actual, err = filepath.EvalSymlinks(actual)
	if err != nil || actual != common {
		return errors.New("recovered checkout did not attach to original repository")
	}
	if _, err := archiveGit(ctx, previous.Path, "fsck", "--full"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	committed = true
	if err := root.RemoveAll(backup); err != nil {
		return err
	}
	return nil
}
