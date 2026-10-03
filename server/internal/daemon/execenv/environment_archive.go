package execenv

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const environmentArchiveDir = ".environment-archive"

// EnvironmentArchiveRequest names a daemon-owned environment. Callers hold its
// execution reservation, process lock and review exclusion until reclamation.
type EnvironmentArchiveRequest struct {
	WorkspacesRoot string
	EnvRoot        string
	ID             string
	Profile        string
	BackendURL     string
	Revision       string
	ExcludedCaches []string
}

// EnvironmentArchive stores a self-contained snapshot and its restore binding.
// The manifest is private to the runtime; public inventory omits Git config.
type EnvironmentArchive struct {
	Version        int                     `json:"version"`
	ID             string                  `json:"id"`
	Profile        string                  `json:"profile"`
	BackendURL     string                  `json:"backend_url"`
	Metadata       *GCMeta                 `json:"metadata,omitempty"`
	ReuseScope     *ManagedEnvProvenance   `json:"reuse_scope,omitempty"`
	Owner          EnvRootOwner            `json:"owner"`
	RelativeRoot   string                  `json:"relative_root"`
	SourceRevision string                  `json:"source_revision"`
	CreatedAt      time.Time               `json:"created_at"`
	PayloadSHA     string                  `json:"payload_sha"`
	PayloadBytes   int64                   `json:"payload_bytes"`
	LogicalBytes   int64                   `json:"logical_bytes"`
	EntryCount     int                     `json:"entry_count"`
	Repositories   []ArchivedGitRepository `json:"repositories"`
	ExcludedCaches []string                `json:"excluded_caches"`
}

func validEnvironmentArchiveID(id string) bool {
	if len(id) != 64 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func environmentArchiveParent(workspacesRoot string) (*os.Root, error) {
	workspace, err := os.OpenRoot(workspacesRoot)
	if err != nil {
		return nil, err
	}
	defer workspace.Close()
	return openReviewArchiveChild(workspace, environmentArchiveDir)
}

// EnvironmentArchiveRevision binds a preview to files and Git state. It never
// follows repository metadata links or changes the source index.
func EnvironmentArchiveRevision(ctx context.Context, path string) (string, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	digest := sha256.New()
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Name() == ".git" || name == envRootLockFile {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeIrregular != 0 {
			return errors.New("environment contains an unsupported filesystem link")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(digest, "%s\x00%d\x00%d\x00%d\n", name, info.Size(), info.Mode(), info.ModTime().UnixNano())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := root.Readlink(filepath.FromSlash(name))
			if err != nil {
				return err
			}
			fmt.Fprintf(digest, "%s\n", target)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	repositories, err := environmentGitRepositories(ctx, root)
	if err != nil {
		return "", err
	}
	for _, relative := range repositories {
		repository := filepath.Join(path, relative)
		for _, args := range [][]string{{"rev-parse", "--verify", "--quiet", "HEAD"}, {"symbolic-ref", "--quiet", "HEAD"}, {"for-each-ref", "--format=%(refname) %(objectname)"}, {"ls-files", "--stage", "-z"}} {
			output, err := archiveGitCommand(ctx, repository, nil, args...).Output()
			if err != nil && args[0] != "rev-parse" && args[0] != "symbolic-ref" {
				return "", err
			}
			fmt.Fprintf(digest, "%s\x00%s\x00%s\n", relative, args[0], output)
		}
		indexPath, err := archiveGit(ctx, repository, "rev-parse", "--path-format=absolute", "--git-path", "index")
		if err != nil {
			return "", err
		}
		index, err := os.ReadFile(filepath.FromSlash(indexPath))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		fmt.Fprintf(digest, "%s\x00index\x00%x\n", relative, sha256.Sum256(index))
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// CaptureEnvironmentArchive publishes only a verified snapshot. It leaves the
// source untouched and never considers a failed capture permission to delete it.
func CaptureEnvironmentArchive(ctx context.Context, request EnvironmentArchiveRequest) (EnvironmentArchive, error) {
	manifest := EnvironmentArchive{Version: 1, ID: request.ID, Profile: request.Profile, BackendURL: request.BackendURL, SourceRevision: request.Revision, CreatedAt: time.Now().UTC(), Repositories: []ArchivedGitRepository{}, ExcludedCaches: request.ExcludedCaches}
	if !validEnvironmentArchiveID(request.ID) || len(request.Revision) != 64 {
		return manifest, errors.New("invalid environment archive identity")
	}
	if err := ctx.Err(); err != nil {
		return manifest, err
	}
	relative, err := filepath.Rel(request.WorkspacesRoot, request.EnvRoot)
	if err != nil || !validArchivedRoot(relative) {
		return manifest, errors.New("invalid managed environment root")
	}
	manifest.RelativeRoot = filepath.ToSlash(relative)
	owner, err := ReadEnvRootOwner(request.EnvRoot)
	if err != nil || owner.TaskID == "" || owner.WorkspaceID == "" {
		return manifest, errors.New("environment owner unavailable")
	}
	manifest.Owner = *owner
	if scope, err := ReadManagedEnvProvenance(request.EnvRoot); err == nil && scope.ManagedBy == ManagedEnvProvenanceManagedBy && scope.WorkspaceID == owner.WorkspaceID {
		manifest.ReuseScope = scope
	}
	if meta, err := ReadGCMeta(request.EnvRoot); err == nil && meta.WorkspaceID == owner.WorkspaceID {
		if meta.TaskID != "" && meta.TaskID != owner.TaskID && meta.LatestTaskID == "" {
			meta.LatestTaskID = meta.TaskID
		}
		meta.TaskID = owner.TaskID
		manifest.Metadata = meta
	}
	source, err := os.OpenRoot(request.EnvRoot)
	if err != nil {
		return manifest, err
	}
	defer source.Close()
	ownerInfo, err := source.Lstat(envRootOwnerFile)
	if err != nil || !ownerInfo.Mode().IsRegular() {
		return manifest, errors.New("environment owner is not a regular file")
	}
	for _, excluded := range request.ExcludedCaches {
		if !filepath.IsLocal(excluded) || excluded == "." {
			return manifest, errors.New("invalid excluded cache path")
		}
	}
	current, err := EnvironmentArchiveRevision(ctx, request.EnvRoot)
	if err != nil || current != request.Revision {
		return manifest, errors.New("environment changed after archive preview")
	}
	parent, err := environmentArchiveParent(request.WorkspacesRoot)
	if err != nil {
		return manifest, err
	}
	defer parent.Close()
	if _, err := parent.Lstat(request.ID); err == nil {
		existing, err := ReadEnvironmentArchive(request.WorkspacesRoot, request.ID)
		if err != nil || existing.SourceRevision != request.Revision || existing.Owner != manifest.Owner || existing.Profile != request.Profile || existing.BackendURL != request.BackendURL {
			return manifest, errors.New("archive operation identity already used")
		}
		if err := VerifyEnvironmentArchive(ctx, request.WorkspacesRoot, request.ID); err != nil {
			return manifest, err
		}
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return manifest, err
	}
	stagingName := ".pending-" + rand.Text()
	staging, err := openReviewArchiveChild(parent, stagingName)
	if err != nil {
		return manifest, err
	}
	defer staging.Close()
	// Incomplete staging directories contain no published manifest and cannot
	// authorize reclamation. Remove them after errors or an atomic publication.
	defer parent.RemoveAll(stagingName)
	stagingPath := filepath.Join(request.WorkspacesRoot, environmentArchiveDir, stagingName)
	repositories, err := environmentGitRepositories(ctx, source)
	if err != nil {
		return manifest, err
	}
	for number, relative := range repositories {
		recovery := filepath.Join(stagingPath, "git", fmt.Sprint(number))
		repository, err := captureGitRepository(ctx, request.EnvRoot, relative, recovery, number)
		if err != nil {
			return manifest, err
		}
		repository.RecoveryPath = fmt.Sprintf("git/%d", number)
		verificationRoot := filepath.Join(stagingPath, "verify", fmt.Sprint(number))
		if err := os.MkdirAll(filepath.Join(verificationRoot, filepath.FromSlash(repository.Path)), 0700); err != nil {
			return manifest, err
		}
		if err := restoreGitRepository(ctx, verificationRoot, recovery, repository); err != nil {
			return manifest, err
		}
		if _, err := archiveGit(ctx, filepath.Join(verificationRoot, filepath.FromSlash(repository.Path)), "fsck", "--full"); err != nil {
			return manifest, fmt.Errorf("verify archived git objects: %w", err)
		}
		manifest.Repositories = append(manifest.Repositories, repository)
	}
	if err := writeEnvironmentArchivePayload(ctx, source, staging, request.ExcludedCaches, &manifest); err != nil {
		return manifest, err
	}
	current, err = EnvironmentArchiveRevision(ctx, request.EnvRoot)
	if err != nil || current != request.Revision {
		return manifest, errors.New("environment changed during archive capture")
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return manifest, err
	}
	if err := writeReviewArchiveFile(staging, "manifest.json", data); err != nil {
		return manifest, err
	}
	if err := verifyEnvironmentArchiveRoot(ctx, staging, manifest); err != nil {
		return manifest, err
	}
	if err := renameArchiveDirectoryNoReplace(parent, stagingName, parent, request.ID); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func validArchivedRoot(relative string) bool {
	parts := strings.Split(filepath.ToSlash(relative), "/")
	return filepath.IsLocal(relative) && len(parts) == 2 && !strings.HasPrefix(parts[0], ".") && parts[0] != "" && parts[1] != "" && parts[1] != "." && parts[1] != ".."
}

// ReadEnvironmentArchive validates identity and paths before exposing a manifest.
func ReadEnvironmentArchive(workspacesRoot, id string) (EnvironmentArchive, error) {
	var manifest EnvironmentArchive
	if !validEnvironmentArchiveID(id) {
		return manifest, errors.New("invalid archive id")
	}
	parent, err := environmentArchiveParent(workspacesRoot)
	if err != nil {
		return manifest, err
	}
	defer parent.Close()
	info, err := parent.Lstat(id)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return manifest, errors.New("archive directory unavailable")
	}
	archive, err := parent.OpenRoot(id)
	if err != nil {
		return manifest, err
	}
	defer archive.Close()
	data, err := readReviewArchiveReceipt(archive, "manifest.json")
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Version != 1 || manifest.ID != id || manifest.Owner.TaskID == "" || manifest.Owner.WorkspaceID == "" || !validArchivedRoot(filepath.FromSlash(manifest.RelativeRoot)) || !validEnvironmentArchiveID(manifest.PayloadSHA) || manifest.PayloadBytes <= 0 || manifest.LogicalBytes < 0 || manifest.EntryCount < 1 {
		return manifest, errors.New("invalid environment archive manifest")
	}
	seen := make(map[string]bool)
	for index, repository := range manifest.Repositories {
		if !filepath.IsLocal(filepath.FromSlash(repository.Path)) || repository.RecoveryPath != fmt.Sprintf("git/%d", index) || seen[repository.Path] || (repository.ObjectFormat != "sha1" && repository.ObjectFormat != "sha256") {
			return manifest, errors.New("invalid archived repository path")
		}
		seen[repository.Path] = true
	}
	return manifest, nil
}

// VerifyEnvironmentArchive checks compressed payload integrity and structure.
func VerifyEnvironmentArchive(ctx context.Context, workspacesRoot, id string) error {
	manifest, err := ReadEnvironmentArchive(workspacesRoot, id)
	if err != nil {
		return err
	}
	parent, err := environmentArchiveParent(workspacesRoot)
	if err != nil {
		return err
	}
	defer parent.Close()
	archive, err := parent.OpenRoot(id)
	if err != nil {
		return err
	}
	defer archive.Close()
	return verifyEnvironmentArchiveRoot(ctx, archive, manifest)
}
