// Package localreview reads local Git review snapshots without contacting a remote.
package localreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const maxOutput = 8 << 20

var ErrTooLarge = errors.New("review output exceeds 8 MiB; narrow the changes before reviewing")

type File struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Patch  string `json:"patch"`
}

type Snapshot struct {
	Committed    bool     `json:"committed"`
	ID           string   `json:"id"`
	Path         string   `json:"path"`
	Branch       string   `json:"branch"`
	Target       string   `json:"target"`
	Head         string   `json:"head"`
	TargetHead   string   `json:"target_head"`
	Base         string   `json:"base"`
	Dirty        bool     `json:"dirty"`
	Branches     []string `json:"branches"`
	Commits      string   `json:"commits"`
	Files        []File   `json:"files"`
	Repositories []string `json:"repositories"`
}

type boundedOutput struct{ data []byte }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > maxOutput {
		return 0, ErrTooLarge
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

// git disables external diff helpers and optional index writes during inspection.
func git(ctx context.Context, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	options := []string{"--no-pager", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-C", path}
	if len(args) > 0 && (args[0] == "status" || args[0] == "diff") {
		filters, err := InspectionFilterOptions(ctx, path)
		if err != nil {
			return "", err
		}
		options = append(options, filters...)
	}
	cmd := exec.CommandContext(ctx, "git", append(options, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_LITERAL_PATHSPECS=1", "GIT_NO_REPLACE_OBJECTS=1")
	out, stderr := &boundedOutput{}, &boundedOutput{}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s failed: %w", args[0], err)
	}
	return string(out.data), nil
}

func trimmed(ctx context.Context, path string, args ...string) (string, error) {
	s, err := git(ctx, path, args...)
	return strings.TrimSpace(s), err
}

// Read compares the merge base to the working tree, including staged and untracked files.
// Callers must authorize the repository path before invoking it.
func Read(ctx context.Context, path, target string) (Snapshot, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Snapshot{}, err
	}
	path = canonical
	s := Snapshot{Path: path, Target: target, Files: []File{}, Branches: []string{}}
	root, err := trimmed(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		repositories, discoveryErr := Repositories(ctx, path)
		if discoveryErr != nil {
			return s, discoveryErr
		}
		if len(repositories) == 1 {
			return Read(ctx, repositories[0], target)
		}
		if len(repositories) == 0 {
			return s, errors.New("no Git repository found in this task directory")
		}
		s.Repositories = repositories
		hash := sha256.Sum256([]byte(strings.Join(repositories, "\x00")))
		s.ID = hex.EncodeToString(hash[:])
		return s, nil
	}
	if filepath.Clean(root) != filepath.Clean(path) {
		return s, errors.New("select the repository root")
	}
	s.Repositories = []string{path}
	refs, err := git(ctx, path, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return s, err
	}
	for _, ref := range strings.Split(strings.TrimSpace(refs), "\n") {
		if ref != "" {
			s.Branches = append(s.Branches, ref)
		}
	}
	valid := false
	for _, ref := range s.Branches {
		if ref == target {
			valid = true
		}
	}
	if !valid {
		return s, errors.New("target must be an existing local branch")
	}
	if s.Branch, err = trimmed(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD"); err != nil {
		return s, errors.New("detached HEAD cannot be reviewed for merging")
	}
	if s.Head, err = trimmed(ctx, path, "rev-parse", "HEAD"); err != nil {
		return s, err
	}
	if s.TargetHead, err = trimmed(ctx, path, "rev-parse", "refs/heads/"+target); err != nil {
		return s, err
	}
	if s.Base, err = trimmed(ctx, path, "merge-base", s.Head, s.TargetHead); err != nil {
		return s, errors.New("branches have no common ancestor")
	}
	status, err := git(ctx, path, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return s, err
	}
	s.Dirty = status != ""
	if s.Commits, err = git(ctx, path, "log", "--format=%H %s", s.TargetHead+".."+s.Head); err != nil {
		return s, err
	}
	names, err := git(ctx, path, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", s.Base, "--")
	if err != nil {
		return s, err
	}
	seen := map[string]bool{}
	total := 0
	for _, name := range strings.Split(names, "\x00") {
		if name == "" {
			continue
		}
		patch, e := git(ctx, path, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--binary", s.Base, "--", name)
		if e != nil {
			return s, e
		}
		if len(patch) > maxOutput-total {
			return s, ErrTooLarge
		}
		total += len(patch)
		s.Files = append(s.Files, File{Path: name, Status: "tracked", Patch: patch})
		seen[name] = true
	}
	untracked, err := git(ctx, path, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return s, err
	}
	for _, name := range strings.Split(untracked, "\x00") {
		if name == "" || seen[name] {
			continue
		}
		info, e := os.Lstat(filepath.Join(path, name))
		if e != nil {
			return s, e
		}
		if !info.Mode().IsRegular() {
			return s, errors.New("untracked symlink or special file cannot be reviewed")
		}
		if info.Size() > maxOutput {
			return s, ErrTooLarge
		}
		root, e := os.OpenRoot(path)
		if e != nil {
			return s, e
		}
		file, e := root.Open(name)
		root.Close()
		if e != nil {
			return s, e
		}
		opened, e := file.Stat()
		if e != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			file.Close()
			return s, errors.New("untracked file changed during inspection")
		}
		data, e := io.ReadAll(io.LimitReader(file, maxOutput+1))
		file.Close()
		if e != nil {
			return s, e
		}
		if len(data) > maxOutput {
			return s, ErrTooLarge
		}
		patch := string(data)
		if strings.ContainsRune(patch, 0) {
			hash := sha256.Sum256(data)
			patch = "Binary file SHA256: " + hex.EncodeToString(hash[:])
		}
		if len(patch) > maxOutput-total {
			return s, ErrTooLarge
		}
		total += len(patch)
		s.Files = append(s.Files, File{Path: name, Status: "untracked", Patch: patch})
	}
	hash := sha256.New()
	for _, value := range []string{path, s.Branch, target, s.Head, s.TargetHead, status} {
		fmt.Fprintf(hash, "%d:%s", len(value), value)
	}
	for _, file := range s.Files {
		fmt.Fprintf(hash, "%d:%s%d:%s", len(file.Path), file.Path, len(file.Patch), file.Patch)
	}
	s.ID = hex.EncodeToString(hash.Sum(nil))
	return s, nil
}
