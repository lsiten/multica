package localreview

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Merge requires the exact previously reviewed clean snapshot. Callers hold
// the daemon environment and repository locks throughout this operation.
func Merge(ctx context.Context, expected Snapshot) (string, error) {
	return MergePrepared(ctx, expected, nil)
}

// MergePrepared durably records the computed merge commit before updating refs.
func MergePrepared(ctx context.Context, expected Snapshot, prepare func(string) error) (string, error) {
	var current Snapshot
	var err error
	if expected.Committed {
		current, err = ReadCommitted(ctx, CommittedRequest{Path: expected.Path, Branch: expected.Branch, Head: expected.Head, Target: expected.Target})
	} else {
		current, err = Read(ctx, expected.Path, expected.Target)
	}
	if err != nil {
		return "", err
	}
	if current.ID != expected.ID {
		return "", errors.New("changes have moved; reload and review again")
	}
	return mergeVerifiedSnapshot(ctx, current, prepare)
}

func mergeVerifiedSnapshot(ctx context.Context, current Snapshot, prepare func(string) error) (string, error) {
	if current.Dirty {
		return "", errors.New("commit local changes before merging")
	}
	if current.Branch == current.Target {
		return "", errors.New("source and target branches must differ for merging")
	}
	count, err := trimmed(ctx, current.Path, "rev-list", "--count", current.TargetHead+".."+current.Head)
	if err != nil {
		return "", err
	}
	if count == "0" {
		return "", errors.New("no commits to merge")
	}
	// Compute the merge in Git's object database, leaving both checkouts untouched on conflict.
	out, err := trimmed(ctx, current.Path, "merge-tree", "--write-tree", current.TargetHead, current.Head)
	if err != nil {
		return "", errors.New("branches conflict; resolve conflicts before merging")
	}
	tree := strings.Split(out, "\n")[0]
	commit, err := trimmed(ctx, current.Path, "commit-tree", tree, "-p", current.TargetHead, "-p", current.Head, "-m", "Merge local review: "+current.Branch+" into "+current.Target)
	if err != nil {
		return "", errors.New("could not create merge commit; check local Git user.name and user.email")
	}
	if prepare != nil {
		if err := prepare(commit); err != nil {
			return "", err
		}
	}
	return commit, publishReviewCommit(ctx, current, commit)
}

func publishReviewCommit(ctx context.Context, current Snapshot, commit string) error {
	targetPath, err := TargetWorktree(ctx, current.Path, current.Target)
	if err != nil {
		return err
	}
	if targetPath == "" {
		_, err = git(ctx, current.Path, "update-ref", "refs/heads/"+current.Target, commit, current.TargetHead)
		return err
	}
	status, err := git(ctx, targetPath, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil || status != "" {
		return errors.New("target checkout has local changes")
	}
	head, err := trimmed(ctx, targetPath, "rev-parse", "HEAD")
	if err != nil || head != current.TargetHead {
		return errors.New("target checkout changed; reload the review")
	}
	branch, err := trimmed(ctx, targetPath, "symbolic-ref", "--short", "HEAD")
	if err != nil || branch != current.Target {
		return errors.New("target checkout switched branches")
	}
	// A local receive-pack transaction validates the exact old ref while Git
	// updates the checked-out tree (updateInstead). A plain merge --ff-only can
	// accept an externally rewound HEAD between our check and its own read.
	return updateCheckedOutTarget(ctx, current.Path, targetPath, current, commit)
}

func updateCheckedOutTarget(ctx context.Context, source, destination string, snapshot Snapshot, commit string) error {
	// The constructed commit has the approved target as its first parent: this
	// operation can only advance that exact target, never rewrite its history.
	parent, err := trimmed(ctx, source, "rev-parse", commit+"^1")
	if err != nil || parent != snapshot.TargetHead {
		return errors.New("merge commit does not descend from the approved target")
	}
	path := filepath.ToSlash(destination)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	localURL := (&url.URL{Scheme: "file", Path: path}).String()
	receiver := "git -c receive.denyCurrentBranch=updateInstead -c core.hooksPath=" + os.DevNull + " receive-pack"
	_, err = git(ctx, source, "-c", "protocol.file.allow=always", "push", "--no-verify", "--receive-pack="+receiver,
		"--force-with-lease=refs/heads/"+snapshot.Target+":"+snapshot.TargetHead,
		localURL, commit+":refs/heads/"+snapshot.Target)
	return err
}

func TargetWorktree(ctx context.Context, path, target string) (string, error) {
	worktrees, err := git(ctx, path, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", err
	}
	candidate := ""
	for _, field := range strings.Split(worktrees, "\x00") {
		if strings.HasPrefix(field, "worktree ") {
			candidate = strings.TrimPrefix(field, "worktree ")
		}
		if field == "branch refs/heads/"+target {
			return candidate, nil
		}
	}
	return "", nil
}

// ContainsMerge verifies an already executed merge after a lost acknowledgement.
func ContainsMerge(ctx context.Context, snapshot Snapshot, commit string) bool {
	if commit == "" {
		return false
	}
	_, err := git(ctx, snapshot.Path, "merge-base", "--is-ancestor", commit, "refs/heads/"+snapshot.Target)
	return err == nil
}

func TargetUnchanged(ctx context.Context, snapshot Snapshot) bool {
	head, err := trimmed(ctx, snapshot.Path, "rev-parse", "refs/heads/"+snapshot.Target)
	return err == nil && head == snapshot.TargetHead
}
