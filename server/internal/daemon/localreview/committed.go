package localreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

type CommittedRequest struct {
	Path   string
	Branch string
	Head   string
	Target string
}

// ReadCommitted reads a pinned delivery without checking out or changing files.
// Unlike a live snapshot it excludes the user's current index and worktree edits.
func ReadCommitted(ctx context.Context, request CommittedRequest) (Snapshot, error) {
	s := Snapshot{Branch: request.Branch, Target: request.Target, Committed: true, Files: []File{}, Repositories: []string{}, Branches: []string{}}
	path, err := filepath.EvalSymlinks(request.Path)
	if err != nil {
		return s, err
	}
	s.Path = path
	if len(request.Head) != 40 && len(request.Head) != 64 {
		return s, errors.New("a pinned source commit is required")
	}
	if _, err := hex.DecodeString(request.Head); err != nil {
		return s, errors.New("invalid source commit")
	}
	refs, err := trimmed(ctx, path, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return s, err
	}
	validTarget := false
	for _, branch := range strings.Split(refs, "\n") {
		if branch != "" {
			s.Branches = append(s.Branches, branch)
		}
		if branch == request.Target {
			validTarget = true
		}
	}
	if !validTarget {
		return s, errors.New("target must be an existing local branch")
	}
	if s.Head, err = trimmed(ctx, path, "rev-parse", "--verify", request.Head+"^{commit}"); err != nil {
		return s, err
	}
	if s.TargetHead, err = trimmed(ctx, path, "rev-parse", "refs/heads/"+request.Target); err != nil {
		return s, err
	}
	if s.Base, err = trimmed(ctx, path, "merge-base", s.Head, s.TargetHead); err != nil {
		return s, err
	}
	if s.Commits, err = git(ctx, path, "log", "--format=%H %s", s.TargetHead+".."+s.Head); err != nil {
		return s, err
	}
	names, err := git(ctx, path, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", s.Base, s.Head, "--")
	if err != nil {
		return s, err
	}
	total := 0
	for _, name := range strings.Split(names, "\x00") {
		if name == "" {
			continue
		}
		patch, e := git(ctx, path, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--binary", s.Base, s.Head, "--", name)
		if e != nil {
			return s, e
		}
		total += len(patch)
		if total > maxOutput {
			return s, ErrTooLarge
		}
		s.Files = append(s.Files, File{Path: name, Status: "tracked", Patch: patch})
	}
	s.Repositories = []string{path}
	data, err := json.Marshal(s)
	if err != nil {
		return s, err
	}
	hash := sha256.Sum256(data)
	s.ID = hex.EncodeToString(hash[:])
	return s, nil
}
