package execenv

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type environmentRemovalRepository struct {
	Common string `json:"common"`
	Path   string `json:"path"`
}
type EnvironmentRemovalReceipt struct {
	Owner        EnvRootOwner                   `json:"owner"`
	RelativeRoot string                         `json:"relative_root"`
	Repositories []environmentRemovalRepository `json:"repositories"`
}

// RemoveEnvironmentDirectory deletes an unused root under the caller's execution
// and repository locks. The temporary tombstone fences path replacement; it is
// removed in the same operation and is never a retained backup.
func RemoveEnvironmentDirectory(ctx context.Context, workspacesRoot, path string, owner EnvRootOwner, revision string) error {
	if err := ValidateEnvRootOwnerPath(workspacesRoot, path, owner); err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return errors.New("environment source is not an owned directory")
	}
	currentOwner, err := ReadEnvRootOwner(path)
	if err != nil || *currentOwner != owner {
		return errors.New("environment owner changed")
	}
	source, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	repositories, err := environmentGitRepositories(ctx, source)
	if closeErr := source.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	linked := []environmentRemovalRepository{}
	for _, relative := range repositories {
		repository := filepath.Join(path, relative)
		common, err := archiveGit(ctx, repository, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return err
		}
		common, err = filepath.EvalSymlinks(filepath.FromSlash(common))
		if err != nil {
			return err
		}
		private, err := archiveGit(ctx, repository, "rev-parse", "--absolute-git-dir")
		if err != nil {
			return err
		}
		private, err = filepath.EvalSymlinks(filepath.FromSlash(private))
		if err != nil {
			return err
		}
		inside, err := filepath.Rel(canonical, common)
		if err != nil {
			return err
		}
		if filepath.IsLocal(inside) {
			worktrees, err := archiveGit(ctx, common, "worktree", "list", "--porcelain", "-z")
			if err != nil {
				return err
			}
			for _, field := range strings.Split(worktrees, "\x00") {
				if !strings.HasPrefix(field, "worktree ") {
					continue
				}
				relative, err := filepath.Rel(canonical, filepath.FromSlash(strings.TrimPrefix(field, "worktree ")))
				if err != nil || !filepath.IsLocal(relative) {
					return errors.New("environment repository is used by another worktree")
				}
			}
		} else if common != private {
			if _, err := os.Lstat(filepath.Join(private, "locked")); err == nil {
				return ErrEnvRootBusy
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			linked = append(linked, environmentRemovalRepository{common, filepath.Join(canonical, relative)})
		}
	}
	currentRevision, err := EnvironmentArchiveRevision(ctx, path)
	if err != nil || currentRevision != revision {
		return errors.New("environment changed before deletion")
	}
	workspace, err := os.OpenRoot(workspacesRoot)
	if err != nil {
		return err
	}
	defer workspace.Close()
	relativeRoot, err := filepath.Rel(workspacesRoot, path)
	if err != nil || !filepath.IsLocal(relativeRoot) {
		return errors.New("environment escaped workspaces root")
	}
	parent, err := workspace.OpenRoot(filepath.Dir(relativeRoot))
	if err != nil {
		return err
	}
	defer parent.Close()
	trash, err := openReviewArchiveChild(workspace, ".environment-trash")
	if err != nil {
		return err
	}
	defer trash.Close()
	tombstone := "cleanup-" + rand.Text()
	receipt := EnvironmentRemovalReceipt{Owner: owner, RelativeRoot: filepath.ToSlash(relativeRoot), Repositories: linked}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err := writeReviewArchiveFile(trash, tombstone+".json", data); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		trash.Remove(tombstone + ".json")
		return err
	}
	if err := renameArchiveDirectoryNoReplace(parent, filepath.Base(path), trash, tombstone); err != nil {
		trash.Remove(tombstone + ".json")
		return err
	}
	moved, err := trash.Lstat(tombstone)
	if err != nil || !os.SameFile(info, moved) {
		if err := renameArchiveDirectoryNoReplace(trash, tombstone, parent, filepath.Base(path)); err != nil {
			return errors.New("environment identity changed; tombstone could not be restored")
		}
		trash.Remove(tombstone + ".json")
		return errors.New("environment identity changed before deletion")
	}
	for _, repository := range linked {
		if _, err := archiveGit(ctx, repository.Common, "worktree", "remove", "--force", repository.Path); err != nil {
			return fmt.Errorf("worktree unregister interrupted: %w", err)
		}
	}
	if err := trash.RemoveAll(tombstone); err != nil {
		return err
	}
	if err := trash.Remove(tombstone + ".json"); err != nil {
		return err
	}
	if _, err := parent.Lstat(filepath.Base(path)); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return RemoveRootDirRecord(workspacesRoot, path, owner)
}

// PendingEnvironmentRemovals exposes only receipts whose original root remains
// inside the managed workspace. Callers revalidate task state before resuming.
func PendingEnvironmentRemovals(workspacesRoot string) (map[string]EnvironmentRemovalReceipt, error) {
	root, err := os.OpenRoot(workspacesRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	trash, err := root.OpenRoot(".environment-trash")
	if errors.Is(err, os.ErrNotExist) {
		return map[string]EnvironmentRemovalReceipt{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer trash.Close()
	entries, err := fs.ReadDir(trash.FS(), ".")
	if err != nil {
		return nil, err
	}
	receipts := map[string]EnvironmentRemovalReceipt{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "cleanup-") || !strings.HasSuffix(entry.Name(), ".json") || !entry.Type().IsRegular() {
			continue
		}
		data, err := readReviewArchiveReceipt(trash, entry.Name())
		if err != nil {
			return nil, err
		}
		var receipt EnvironmentRemovalReceipt
		if json.Unmarshal(data, &receipt) != nil || !validArchivedRoot(filepath.FromSlash(receipt.RelativeRoot)) {
			return nil, errors.New("invalid environment deletion receipt")
		}
		path := filepath.Join(workspacesRoot, filepath.FromSlash(receipt.RelativeRoot))
		if err := ValidateEnvRootOwnerPath(workspacesRoot, path, receipt.Owner); err != nil {
			return nil, err
		}
		canonicalRoot, err := filepath.EvalSymlinks(workspacesRoot)
		if err != nil {
			return nil, err
		}
		original := filepath.Join(canonicalRoot, filepath.FromSlash(receipt.RelativeRoot))
		for _, repository := range receipt.Repositories {
			relative, err := filepath.Rel(original, repository.Path)
			if err != nil || !filepath.IsLocal(relative) || relative == "." || !filepath.IsAbs(repository.Common) {
				return nil, errors.New("invalid deletion repository binding")
			}
		}
		receipts[strings.TrimSuffix(entry.Name(), ".json")] = receipt
	}
	return receipts, nil
}

// FinishEnvironmentRemoval resumes committed deletion without recreating a
// checkout or registering a recovery archive.
func FinishEnvironmentRemoval(ctx context.Context, workspacesRoot, id string, receipt EnvironmentRemovalReceipt) error {
	if !strings.HasPrefix(id, "cleanup-") || strings.ContainsAny(id, "/\\") {
		return errors.New("invalid deletion tombstone")
	}
	root, err := os.OpenRoot(workspacesRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	trash, err := root.OpenRoot(".environment-trash")
	if err != nil {
		return err
	}
	defer trash.Close()
	original := filepath.Join(workspacesRoot, filepath.FromSlash(receipt.RelativeRoot))
	if !validArchivedRoot(filepath.FromSlash(receipt.RelativeRoot)) {
		return errors.New("invalid environment deletion root")
	}
	if err := ValidateEnvRootOwnerPath(workspacesRoot, original, receipt.Owner); err != nil {
		return err
	}
	info, err := trash.Lstat(id)
	if errors.Is(err, os.ErrNotExist) {
		// The receipt can survive a crash before rename or after removal.
		// Without a tombstone there is no committed source to unregister.
		return trash.Remove(id + ".json")
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid environment deletion tombstone")
	}
	owner, err := ReadEnvRootOwner(filepath.Join(workspacesRoot, ".environment-trash", id))
	if err != nil || *owner != receipt.Owner {
		return errors.New("environment deletion owner changed")
	}
	unlockRepositories, err := lockRemovalRepositories(ctx, receipt.Repositories)
	if err != nil {
		return err
	}
	defer unlockRepositories()
	for _, repository := range receipt.Repositories {
		if err := ctx.Err(); err != nil {
			return err
		}
		registered, err := archiveGit(ctx, repository.Common, "worktree", "list", "--porcelain", "-z")
		if err != nil {
			return err
		}
		found := false
		for _, field := range strings.Split(registered, "\x00") {
			if field == "worktree "+repository.Path {
				found = true
			}
		}
		if found {
			if _, err := os.Lstat(repository.Path); err == nil {
				// A replacement owns the original path and its registration.
				// Only the fenced old directory is ours to remove.
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if _, err := archiveGit(ctx, repository.Common, "worktree", "remove", "--force", repository.Path); err != nil {
				return err
			}
		}
	}
	if err := trash.RemoveAll(id); err != nil {
		return err
	}
	if err := trash.Remove(id + ".json"); err != nil {
		return err
	}
	if _, err := os.Lstat(original); errors.Is(err, os.ErrNotExist) {
		return RemoveRootDirRecord(workspacesRoot, original, receipt.Owner)
	}
	return nil
}

func lockRemovalRepositories(ctx context.Context, repositories []environmentRemovalRepository) (func(), error) {
	unique := map[string]bool{}
	for _, repository := range repositories {
		unique[repository.Common] = true
	}
	paths := make([]string, 0, len(unique))
	for path := range unique {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	locks := []*os.File{}
	release := func() {
		for _, lock := range locks {
			releaseLockFile(lock)
		}
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		lock, err := openLockFile(filepath.Join(path, gitRootLockFileName))
		if err != nil {
			release()
			return nil, err
		}
		locked, err := lockFileExclusiveNonBlocking(lock)
		if err != nil || !locked {
			lock.Close()
			release()
			if err != nil {
				return nil, err
			}
			return nil, ErrEnvRootBusy
		}
		locks = append(locks, lock)
	}
	return release, nil
}
