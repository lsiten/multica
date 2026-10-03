package execenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type archivedGitRef struct {
	Name string `json:"name"`
	OID  string `json:"oid"`
}

type archivedGitConfig struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ArchivedGitRepository is sufficient to reconstruct Git without depending on
// the original repository, worktree registration, alternates, or object cache.
type ArchivedGitRepository struct {
	Path         string              `json:"path"`
	RecoveryPath string              `json:"recovery_path"`
	Head         string              `json:"head"`
	Branch       string              `json:"branch"`
	ObjectFormat string              `json:"object_format"`
	Refs         []archivedGitRef    `json:"refs"`
	Config       []archivedGitConfig `json:"config"`
	CommonDir    string              `json:"common_dir"`
	Linked       bool                `json:"linked"`
}

func archiveGitCommand(ctx context.Context, repository string, input io.Reader, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "gc.auto=0", "-c", "maintenance.auto=false", "-C", repository}, args...)...)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1")
	cmd.Stdin = input
	return cmd
}

func archiveGit(ctx context.Context, repository string, args ...string) (string, error) {
	output, err := archiveGitCommand(ctx, repository, nil, args...).Output()
	if err != nil {
		return "", fmt.Errorf("archive git %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(output)), nil
}

func environmentGitRepositories(ctx context.Context, source *os.Root) ([]string, error) {
	repositories := []string{}
	err := fs.WalkDir(source.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 && entry.IsDir() {
			return fs.SkipDir
		}
		if entry.Name() != ".git" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("git metadata is a symbolic link")
		}
		repositories = append(repositories, filepath.FromSlash(filepath.Dir(path)))
		if entry.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
	return repositories, err
}

func captureGitRepository(ctx context.Context, sourcePath, relative, recovery string, number int) (ArchivedGitRepository, error) {
	repository := filepath.Join(sourcePath, relative)
	result := ArchivedGitRepository{Path: filepath.ToSlash(relative), RecoveryPath: fmt.Sprintf(".environment-git/%d", number), Refs: []archivedGitRef{}, Config: []archivedGitConfig{}}
	canonical, err := filepath.EvalSymlinks(repository)
	if err != nil {
		return result, err
	}
	top, err := archiveGit(ctx, repository, "rev-parse", "--show-toplevel")
	if err != nil || filepath.Clean(filepath.FromSlash(top)) != canonical {
		return result, errors.New("repository is outside the managed environment")
	}
	result.ObjectFormat, err = archiveGit(ctx, repository, "rev-parse", "--show-object-format")
	if err != nil || (result.ObjectFormat != "sha1" && result.ObjectFormat != "sha256") {
		return result, errors.New("unsupported git object format")
	}
	gitDir, err := archiveGit(ctx, repository, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return result, err
	}
	result.CommonDir, err = archiveGit(ctx, repository, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return result, err
	}
	result.CommonDir = filepath.Clean(filepath.FromSlash(result.CommonDir))
	gitDir = filepath.Clean(filepath.FromSlash(gitDir))
	result.Linked = result.CommonDir != gitDir
	if _, err := os.Lstat(filepath.Join(gitDir, "locked")); err == nil {
		return result, errors.New("git worktree is locked")
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	result.Head, err = archiveGit(ctx, repository, "rev-parse", "--verify", "HEAD")
	if err != nil {
		if _, branchErr := archiveGit(ctx, repository, "symbolic-ref", "HEAD"); branchErr != nil {
			return result, err
		}
		result.Head = ""
	}
	result.Branch, _ = archiveGit(ctx, repository, "symbolic-ref", "--quiet", "HEAD") // Detached HEAD is valid.
	refs, err := archiveGit(ctx, repository, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		return result, err
	}
	for _, line := range strings.Split(refs, "\n") {
		if name, oid, ok := strings.Cut(line, " "); ok {
			result.Refs = append(result.Refs, archivedGitRef{Name: name, OID: oid})
		}
	}
	if err := os.MkdirAll(recovery, 0700); err != nil {
		return result, err
	}
	indexPath := filepath.Join(gitDir, "index")
	index, err := os.ReadFile(indexPath)
	if err == nil {
		privateIndex := filepath.Join(recovery, "index")
		if err := os.WriteFile(privateIndex, index, 0600); err != nil {
			return result, err
		}
		cmd := archiveGitCommand(ctx, repository, nil, "update-index", "--no-split-index")
		cmd.Env = append(cmd.Env, "GIT_INDEX_FILE="+privateIndex)
		if err := cmd.Run(); err != nil {
			return result, fmt.Errorf("materialize archived index: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	staged, err := archiveGitCommand(ctx, repository, nil, "ls-files", "--stage", "-z").Output()
	if err != nil {
		return result, err
	}
	objects := make(map[string]bool)
	if result.Head != "" {
		objects[result.Head] = true
	}
	oidLength := 40
	if result.ObjectFormat == "sha256" {
		oidLength = 64
	}
	operationObjects, err := captureArchivedGitMetadata(ctx, result.CommonDir, gitDir, recovery, oidLength)
	if err != nil {
		return result, err
	}
	for _, oid := range operationObjects {
		objects[oid] = true
	}
	for _, entry := range strings.Split(string(staged), "\x00") {
		fields := strings.Fields(strings.SplitN(entry, "\t", 2)[0])
		if len(fields) == 3 && fields[0] != "160000" && strings.Trim(fields[1], "0") != "" {
			objects[fields[1]] = true
		}
	}
	objectIDs := make([]string, 0, len(objects))
	for oid := range objects {
		objectIDs = append(objectIDs, oid)
	}
	sort.Strings(objectIDs)
	pack, err := os.OpenFile(filepath.Join(recovery, "objects.pack"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	input := strings.Join(objectIDs, "\n")
	if input != "" {
		input += "\n"
	}
	cmd := archiveGitCommand(ctx, repository, strings.NewReader(input), "pack-objects", "--stdout", "--revs", "--all", "--reflog", "--include-tag")
	cmd.Stdout = pack
	runErr := cmd.Run()
	if err := errors.Join(runErr, pack.Sync(), pack.Close()); err != nil {
		return result, fmt.Errorf("capture git objects: %w", err)
	}
	for _, name := range []string{"shallow", "info/exclude", "info/attributes", "info/sparse-checkout"} {
		data, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(name)))
		if errors.Is(err, os.ErrNotExist) && gitDir != result.CommonDir {
			data, err = os.ReadFile(filepath.Join(result.CommonDir, filepath.FromSlash(name)))
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return result, err
		}
		destination := filepath.Join(recovery, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return result, err
		}
		if err := os.WriteFile(destination, data, 0600); err != nil {
			return result, err
		}
	}
	config, err := archiveGitCommand(ctx, repository, nil, "config", "--null", "--list").Output()
	if err != nil {
		return result, err
	}
	for _, record := range bytes.Split(config, []byte{0}) {
		name, value, ok := strings.Cut(string(record), "\n")
		if ok && archivedConfigAllowed(name) {
			result.Config = append(result.Config, archivedGitConfig{Name: name, Value: value})
		}
	}
	return result, nil
}

func archivedConfigAllowed(name string) bool {
	switch name {
	case "core.autocrlf", "core.eol", "core.filemode", "core.ignorecase", "core.symlinks", "core.sparsecheckout", "core.sparsecheckoutcone", "user.name", "user.email":
		return true
	}
	return strings.HasPrefix(name, "remote.") && (strings.HasSuffix(name, ".url") || strings.HasSuffix(name, ".fetch") || strings.HasSuffix(name, ".pushurl"))
}

func restoreGitRepository(ctx context.Context, destination, recovery string, repository ArchivedGitRepository) error {
	path := filepath.Join(destination, filepath.FromSlash(repository.Path))
	if _, err := archiveGit(ctx, path, "init", "--object-format="+repository.ObjectFormat); err != nil {
		return err
	}
	for _, name := range []string{"shallow", "info/exclude", "info/attributes", "info/sparse-checkout"} {
		data, err := os.ReadFile(filepath.Join(recovery, filepath.FromSlash(name)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		target := filepath.Join(path, ".git", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			return err
		}
	}
	pack, err := os.Open(filepath.Join(recovery, "objects.pack"))
	if err != nil {
		return err
	}
	cmd := archiveGitCommand(ctx, path, pack, "index-pack", "--stdin", "--strict")
	if err := errors.Join(cmd.Run(), pack.Close()); err != nil {
		return fmt.Errorf("restore git objects: %w", err)
	}
	for _, ref := range repository.Refs {
		if _, err := archiveGit(ctx, path, "update-ref", ref.Name, ref.OID); err != nil {
			return err
		}
	}
	if repository.Branch != "" {
		if _, err := archiveGit(ctx, path, "symbolic-ref", "HEAD", repository.Branch); err != nil {
			return err
		}
	} else if repository.Head != "" {
		if _, err := archiveGit(ctx, path, "update-ref", "--no-deref", "HEAD", repository.Head); err != nil {
			return err
		}
	}
	index, err := os.ReadFile(filepath.Join(recovery, "index"))
	if err == nil {
		if err := os.WriteFile(filepath.Join(path, ".git", "index"), index, 0600); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, config := range repository.Config {
		if !archivedConfigAllowed(config.Name) {
			return errors.New("archive contains unsafe git configuration")
		}
		if _, err := archiveGit(ctx, path, "config", "--add", config.Name, config.Value); err != nil {
			return err
		}
	}
	gitDirCache.Delete(path)
	gitCommonDirCache.Delete(path)
	return restoreArchivedGitMetadata(ctx, recovery, path)
}
