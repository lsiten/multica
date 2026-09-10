package localreview

import (
	"context"
	"errors"
	"slices"
	"strings"
)

var ErrSelectedMergeConflict = errors.New("selected file changes conflict with the target; resolve before merging")
var ErrSelectedMergeEmpty = errors.New("selected files have no changes left to apply to the target")

type SelectedMergeRequest struct {
	Version VersionSelection
	Paths   []string
	Message string
}

// MergeSelectedPrepared makes a single-parent target commit. It intentionally
// does not record the whole source branch as merged: unselected commits/files
// must remain eligible for a later review. Callers own runtime/repository locks.
func (s *BlobStore) MergeSelectedPrepared(ctx context.Context, request SelectedMergeRequest, prepare func(string) error) (commit string, err error) {
	if len(request.Paths) == 0 || len(request.Paths) > maxVersionFiles || prepare == nil || strings.TrimSpace(request.Message) == "" || len(request.Message) > 8000 || strings.ContainsRune(request.Message, 0) {
		return "", ErrInvalidReviewVersion
	}
	version, err := s.VerifyVersion(ctx, request.Version)
	if err != nil {
		return "", err
	}
	h := version.Header
	if h.Dirty || h.Branch == h.Target {
		return "", ErrInvalidReviewVersion
	}
	tx, err := openIndexTransaction(ctx, h.Repository)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, tx.Close()) }()
	if _, err := tx.git(ctx, nil, "read-tree", h.Base); err != nil {
		return "", err
	}
	seen := make(map[string]bool)
	for _, path := range request.Paths {
		if !validVersionPath(path) || seen[path] {
			return "", ErrInvalidReviewVersion
		}
		seen[path] = true
		index := slices.IndexFunc(version.Files, func(file VersionFile) bool { return file.Path == path })
		if index < 0 {
			return "", ErrInvalidReviewVersion
		}
		file := version.Files[index]
		if file.NewMode == "000000" {
			if _, err := tx.git(ctx, nil, "update-index", "--force-remove", "--", path); err != nil {
				return "", err
			}
			continue
		}
		oid, err := trimmed(ctx, h.Repository, "rev-parse", "--verify", h.Head+":"+path)
		if err != nil || !validGitObjectID(oid) {
			return "", ErrInvalidReviewVersion
		}
		if _, err := tx.git(ctx, nil, "update-index", "--add", "--cacheinfo", file.NewMode, oid, path); err != nil {
			return "", err
		}
	}
	tree, err := tx.git(ctx, nil, "write-tree")
	if err != nil {
		return "", err
	}
	selected, err := tx.git(ctx, strings.NewReader("Selected file comparison\n"), "commit-tree", tree, "-p", h.Base, "-F", "-")
	if err != nil {
		return "", err
	}
	merged, err := tx.git(ctx, nil, "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", h.TargetHead, selected)
	if err != nil {
		return "", selectedConflict(merged)
	}
	mergedTree := strings.Split(merged, "\x00")[0]
	if !validGitObjectID(mergedTree) {
		return "", ErrInvalidReviewVersion
	}
	targetTree, err := tx.git(ctx, nil, "rev-parse", h.TargetHead+"^{tree}")
	if err != nil {
		return "", err
	}
	if targetTree == mergedTree {
		return "", ErrSelectedMergeEmpty
	}
	commit, err = tx.git(ctx, strings.NewReader(request.Message+"\n"), "commit-tree", mergedTree, "-p", h.TargetHead, "-F", "-")
	if err != nil {
		return "", err
	}
	if !validGitObjectID(commit) {
		return "", ErrInvalidReviewVersion
	}
	if err := tx.validate(ctx); err != nil {
		return "", err
	}
	if err := tx.Close(); err != nil {
		return "", err
	}
	if err := prepare(commit); err != nil {
		return "", err
	}
	return commit, publishReviewCommit(ctx, version.RecoverySnapshot(request.Version.ID), commit)
}
