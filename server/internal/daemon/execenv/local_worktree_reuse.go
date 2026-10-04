package execenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const retainedLocalWorktreeFile = ".local_worktree_reuse.json"

var ErrLocalWorktreeNotReusable = errors.New("retained branch is no longer continuable")

func writeRetainedLocalWorktree(worktree *LocalWorktree) error {
	data, err := json.Marshal(worktree)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(filepath.Dir(worktree.Path), retainedLocalWorktreeFile), data, 0600)
}

func ReadRetainedLocalWorktree(root string) (*LocalWorktree, error) {
	file := filepath.Join(root, retainedLocalWorktreeFile)
	info, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return nil, errors.New("invalid retained local worktree record")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var worktree LocalWorktree
	if json.Unmarshal(data, &worktree) != nil || !worktree.RetainCheckout || !worktree.owner.valid() {
		return nil, errors.New("retained local worktree binding invalid")
	}
	expected, err := filepath.EvalSymlinks(filepath.Join(root, localWorktreeDirName))
	if err != nil {
		return nil, err
	}
	actual, err := filepath.EvalSymlinks(worktree.Path)
	if err != nil || actual != expected {
		return nil, errors.New("retained local worktree path changed")
	}
	return &worktree, nil
}

func LocalWorktreeReuseMatches(previous *LocalWorktree, params LocalWorktreeParams) bool {
	root, err := resolveGitRoot(params.LocalPath)
	return err == nil && root == previous.GitRoot && previous.owner == params.owner()
}

// FindRetainedLocalWorktree locates the checkout of this workline's owned branch.
// The caller must validate managed provenance and hold its execution claim before reuse.
func FindRetainedLocalWorktree(params LocalWorktreeParams, logger *slog.Logger) (string, error) {
	gitRoot, err := resolveGitRoot(params.LocalPath)
	if err != nil {
		return "", err
	}
	unlock, err := lockGitRoot(gitRoot, logger)
	if err != nil {
		return "", err
	}
	defer unlock()
	head, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	plan := resolveTaskBranch(gitRoot, params, head, logger)
	if !plan.continues {
		return "", nil
	}
	worktrees, err := runGitStdout(gitRoot, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", err
	}
	for _, record := range strings.Split(worktrees, "\x00\x00") {
		path, branch := "", ""
		for _, field := range strings.Split(record, "\x00") {
			if strings.HasPrefix(field, "worktree ") {
				path = strings.TrimPrefix(field, "worktree ")
			} else if strings.HasPrefix(field, "branch ") {
				branch = strings.TrimPrefix(field, "branch ")
			}
		}
		if path == "" || branch != "refs/heads/"+plan.name {
			continue
		}
		previous, err := ReadRetainedLocalWorktree(filepath.Dir(path))
		if err == nil && previous.Branch == plan.name && LocalWorktreeReuseMatches(previous, params) {
			return previous.WorkDir, nil
		}
	}
	return "", nil
}

// ReuseLocalWorktree runs while the caller owns the old environment lease.
// It replays user edits using the same merge rules as a fresh checkout.
func ReuseLocalWorktree(previous *LocalWorktree, params LocalWorktreeParams, logger *slog.Logger) (*LocalWorktree, error) {
	gitRoot, err := resolveGitRoot(params.LocalPath)
	if err != nil {
		return nil, err
	}
	if gitRoot != previous.GitRoot || previous.owner != params.owner() {
		return nil, errors.New("local worktree scope changed")
	}
	common, err := runGitTrimmed(previous.Path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	originCommon, err := runGitTrimmed(gitRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return nil, err
	}
	originCommon, err = filepath.EvalSymlinks(originCommon)
	if err != nil || common != originCommon {
		return nil, ErrLocalWorktreeNotReusable
	}
	unlock, err := lockGitRoot(gitRoot, logger)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if params.SharedCheckout {
		// Replaying a user snapshot into an actively edited checkout would
		// overwrite the other run's work. Continue its current state instead.
		branch, err := runGitTrimmed(previous.Path, "symbolic-ref", "--short", "HEAD")
		if err != nil || branch != previous.Branch {
			return nil, errors.New("shared worktree branch changed")
		}
		if _, owned := branchOwnedBy(gitRoot, branch, params.owner(), logger); !owned {
			return nil, ErrLocalWorktreeNotReusable
		}
		tip, err := runGitTrimmed(previous.Path, "rev-parse", "--verify", "HEAD")
		if err != nil {
			return nil, err
		}
		next := *previous
		next.BaseCommit, next.Continued, next.createdBranch = tip, true, false
		return &next, nil
	}
	if dirty, err := worktreeIsDirty(previous.Path); err != nil || dirty {
		return nil, errors.New("retained local worktree has uncommitted changes")
	}
	head, err := runGitTrimmed(gitRoot, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return nil, err
	}
	plan := resolveTaskBranch(gitRoot, params, head, logger)
	if plan.name != previous.Branch || !plan.continues {
		return nil, ErrLocalWorktreeNotReusable
	}
	branch, err := runGitTrimmed(previous.Path, "symbolic-ref", "--short", "HEAD")
	if err != nil || branch != previous.Branch {
		return nil, errors.New("retained worktree branch changed")
	}
	if err := checkUntrackedReplayable(gitRoot, logger); err != nil {
		return nil, err
	}
	state, err := captureUserSnapshot(gitRoot, params.EnvRoot, head, logger)
	if err != nil {
		return nil, err
	}
	replay, err := replayUserState(previous.Path, plan, state, logger)
	if err != nil {
		return nil, err
	}
	next := &LocalWorktree{GitRoot: gitRoot, Path: previous.Path, WorkDir: previous.WorkDir, Branch: previous.Branch, BaseCommit: plan.base, Continued: true, RetainCheckout: true, ReplayConflicts: replay.conflicts,
		userState: state, priorState: plan.priorState, owner: plan.owner, tracksState: true, snapshotPending: len(replay.conflicts) > 0}
	if len(replay.conflicts) == 0 {
		if dirty, err := worktreeIsDirty(next.Path); err != nil {
			return nil, err
		} else if dirty {
			baseline, err := commitBaseline(next.Path, true, true)
			if err != nil {
				return nil, fmt.Errorf("record reused worktree baseline: %w", err)
			}
			next.BaseCommit = baseline
		}
		if err := next.recordState(next.BaseCommit, logger); err != nil {
			return nil, err
		}
	}
	if err := writeRetainedLocalWorktree(next); err != nil {
		return nil, fmt.Errorf("record reused local worktree: %w", err)
	}
	return next, nil
}
