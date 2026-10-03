package execenv

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LockEnvironmentArchiveRepositories excludes Multica's cross-process Git
// administration. Locks are deduplicated by common directory and never waited
// on indefinitely; a busy repository retains its working environment.
func LockEnvironmentArchiveRepositories(ctx context.Context, path string) (func(), error) {
	source, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	repositories, err := environmentGitRepositories(ctx, source)
	if err != nil {
		return nil, err
	}
	commonDirectories := make(map[string]bool)
	for _, repository := range repositories {
		common, err := archiveGit(ctx, filepath.Join(path, repository), "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return nil, err
		}
		canonical, err := filepath.EvalSymlinks(filepath.FromSlash(common))
		if err != nil {
			return nil, err
		}
		commonDirectories[canonical] = true
	}
	directories := make([]string, 0, len(commonDirectories))
	for directory := range commonDirectories {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	locks := []*os.File{}
	release := func() {
		for _, lock := range locks {
			releaseLockFile(lock)
		}
	}
	for _, directory := range directories {
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		lock, err := openLockFile(filepath.Join(directory, gitRootLockFileName))
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

// ReclaimEnvironmentArchive requires the same execution and Git leases as
// capture. Moving to a unique tombstone first prevents removal of a newly
// created environment at the original path.
func ReclaimEnvironmentArchive(ctx context.Context, workspacesRoot, id string) error {
	manifest, err := ReadEnvironmentArchive(workspacesRoot, id)
	if err != nil {
		return err
	}
	if err := VerifyEnvironmentArchive(ctx, workspacesRoot, id); err != nil {
		return err
	}
	path := filepath.Join(workspacesRoot, filepath.FromSlash(manifest.RelativeRoot))
	canonicalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	sourceInfo, err := os.Lstat(path)
	if err != nil || !sourceInfo.IsDir() || sourceInfo.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return errors.New("archive source is not a real environment directory")
	}
	owner, err := ReadEnvRootOwner(path)
	if err != nil || *owner != manifest.Owner {
		return errors.New("archive source owner changed")
	}
	for _, repository := range manifest.Repositories {
		common, err := archiveGit(ctx, filepath.Join(path, filepath.FromSlash(repository.Path)), "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil || filepath.Clean(filepath.FromSlash(common)) != repository.CommonDir {
			return errors.New("archive source repository binding changed")
		}
		relativeCommon, err := filepath.Rel(canonicalPath, repository.CommonDir)
		if err != nil {
			return err
		}
		if filepath.IsLocal(relativeCommon) {
			worktrees, err := archiveGit(ctx, repository.CommonDir, "worktree", "list", "--porcelain", "-z")
			if err != nil {
				return err
			}
			for _, entry := range strings.Split(worktrees, "\x00") {
				if !strings.HasPrefix(entry, "worktree ") {
					continue
				}
				relativeWorktree, err := filepath.Rel(canonicalPath, filepath.FromSlash(strings.TrimPrefix(entry, "worktree ")))
				if err != nil || !filepath.IsLocal(relativeWorktree) {
					return errors.New("environment contains a shared repository used by another worktree")
				}
			}
		}
	}
	revision, err := EnvironmentArchiveRevision(ctx, path)
	if err != nil || revision != manifest.SourceRevision {
		return errors.New("archive source changed before reclamation")
	}
	workspace, err := os.OpenRoot(workspacesRoot)
	if err != nil {
		return err
	}
	defer workspace.Close()
	parentName := filepath.Dir(filepath.FromSlash(manifest.RelativeRoot))
	info, err := workspace.Lstat(parentName)
	if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return errors.New("archive source workspace unavailable")
	}
	sourceParent, err := workspace.OpenRoot(parentName)
	if err != nil {
		return err
	}
	defer sourceParent.Close()
	opened, err := sourceParent.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("archive source workspace changed")
	}
	trash, err := openReviewArchiveChild(workspace, ".environment-trash")
	if err != nil {
		return err
	}
	defer trash.Close()
	trashName := id + "-" + rand.Text()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := renameArchiveDirectoryNoReplace(sourceParent, filepath.Base(path), trash, trashName); err != nil {
		return err
	}
	movedInfo, err := trash.Lstat(trashName)
	if err != nil || !os.SameFile(sourceInfo, movedInfo) {
		if rollbackErr := renameArchiveDirectoryNoReplace(trash, trashName, sourceParent, filepath.Base(path)); rollbackErr != nil {
			return errors.New("archive source identity changed; recovery tombstone retained")
		}
		return errors.New("archive source identity changed before reclamation")
	}
	// The verified archive and tombstone both remain recoverable if Git admin
	// cleanup fails. Do not prune unrelated worktree registrations.
	for _, repository := range manifest.Repositories {
		if !repository.Linked {
			continue
		}
		relativeCommon, err := filepath.Rel(canonicalPath, repository.CommonDir)
		if err != nil {
			return err
		}
		if filepath.IsLocal(relativeCommon) {
			continue
		}
		original := filepath.Join(canonicalPath, filepath.FromSlash(repository.Path))
		if _, err := archiveGit(ctx, repository.CommonDir, "worktree", "remove", "--force", "--force", original); err != nil {
			return errors.New("archived worktree registration cleanup failed; snapshot and tombstone retained")
		}
	}
	if err := trash.RemoveAll(trashName); err != nil {
		return err
	}
	return RemoveRootDirRecord(workspacesRoot, path, manifest.Owner)
}
