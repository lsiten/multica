package localreview

import (
	"context"
	"errors"
	"path/filepath"
)

// CaptureUnstaged freezes the index-to-working comparison independently of the
// original MR target and the HEAD-to-index staged comparison.
func (s *BlobStore) CaptureUnstaged(ctx context.Context, repository, expectedIndex string) (version ReviewVersion, err error) {
	path, err := filepath.EvalSymlinks(repository)
	if err != nil {
		return version, err
	}
	tx, err := openIndexTransaction(ctx, path)
	if err != nil {
		return version, err
	}
	defer func() { err = errors.Join(err, tx.Close()) }()
	if tx.original != expectedIndex {
		return version, ErrIndexStateChanged
	}
	tree, err := tx.git(ctx, nil, "write-tree")
	if err != nil {
		return version, err
	}
	version, err = s.CaptureWorking(ctx, WorkingVersionRequest{Path: path, Target: tx.branch, indexBase: tree})
	if err != nil {
		return ReviewVersion{}, err
	}
	if err := tx.validate(ctx); err != nil {
		return ReviewVersion{}, err
	}
	return version, nil
}

// CaptureStaged reads a locked index copy. It never stages the worktree or
// creates a commit/ref; write-tree only freezes the index's Git tree objects.
func (s *BlobStore) CaptureStaged(ctx context.Context, repository, expectedIndex string) (version ReviewVersion, err error) {
	path, err := filepath.EvalSymlinks(repository)
	if err != nil {
		return version, err
	}
	tx, err := openIndexTransaction(ctx, path)
	if err != nil {
		return version, err
	}
	defer func() { err = errors.Join(err, tx.Close()) }()
	if tx.original != expectedIndex {
		return version, ErrIndexStateChanged
	}
	tree, err := tx.git(ctx, nil, "write-tree")
	if err != nil {
		return version, err
	}
	if !validGitObjectID(tree) {
		return version, ErrInvalidReviewVersion
	}
	raw, err := tx.git(ctx, nil, "diff", "--cached", "--raw", "-z", "--no-abbrev", "--no-renames", "--no-ext-diff", "--no-textconv", tx.head, "--")
	if err != nil {
		return version, err
	}
	files, err := parseRawVersionFiles(raw)
	if err != nil {
		return version, err
	}
	stats, err := tx.git(ctx, nil, "diff", "--cached", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", tx.head, "--")
	if err != nil {
		return version, err
	}
	if err := applyVersionStats(files, stats); err != nil {
		return version, err
	}
	for index := range files {
		file := &files[index]
		for _, side := range []struct {
			oid, mode string
			ref       **BlobRef
			cached    *bool
		}{
			{file.OldOID, file.OldMode, &file.Old, &file.OldCached}, {file.NewOID, file.NewMode, &file.New, &file.NewCached},
		} {
			if side.mode == "000000" {
				continue
			}
			if side.mode == "160000" {
				file.Preview = "unsupported"
				continue
			}
			blob, binary, err := s.captureObject(ctx, gitObjectRequest{Repository: path, OID: side.oid})
			if errors.Is(err, ErrSnapshotBlobTooLarge) || errors.Is(err, ErrSnapshotCacheFull) {
				if file.Preview != "unsupported" {
					file.Preview = "uncached"
				}
				continue
			}
			if err != nil {
				return version, err
			}
			*side.ref, *side.cached = &blob, true
			if binary && file.Preview == "text" {
				file.Preview = "binary"
			}
		}
	}
	version = ReviewVersion{Format: 2, Header: VersionHeader{Repository: path, Branch: tx.branch, Target: tx.branch, Head: tx.head, TargetHead: tx.head, Base: tx.head, IndexTree: tree, Dirty: true}, Files: files}
	if err := tx.validate(ctx); err != nil {
		return ReviewVersion{}, err
	}
	return version, validateReviewVersion(version)
}
