package localreview

import (
	"context"
	"errors"
)

type VersionSelection struct{ ID, Path, Target string }

// VerifyVersion checks immutable review identity against the current repository,
// scoped to the already-authorized path and target supplied by the caller.
func (s *BlobStore) VerifyVersion(ctx context.Context, selection VersionSelection) (ReviewVersion, error) {
	expected, err := s.LoadVersion(ctx, selection.ID)
	if err != nil {
		return ReviewVersion{}, err
	}
	header := expected.Header
	if header.Repository != selection.Path || header.Target != selection.Target {
		return ReviewVersion{}, ErrInvalidReviewVersion
	}
	var current ReviewVersion
	if header.Committed {
		current, err = s.CaptureCommitted(ctx, CommittedRequest{Path: header.Repository, Branch: header.Branch, Head: header.Head, Target: header.Target})
	} else {
		current, err = s.CaptureWorking(ctx, WorkingVersionRequest{Path: header.Repository, Target: header.Target})
	}
	if err != nil {
		return ReviewVersion{}, err
	}
	currentID, err := s.SaveVersion(ctx, current)
	if err != nil {
		return ReviewVersion{}, err
	}
	if currentID != selection.ID {
		return ReviewVersion{}, errors.New("changes have moved; reload and review again")
	}
	return expected, nil
}

// MergeVersionPrepared reuses the existing exact-ref merge transaction after
// content-based revalidation, without materializing an aggregate patch.
// The caller holds the same environment/source/repository locks as legacy merge.
func (s *BlobStore) MergeVersionPrepared(ctx context.Context, selection VersionSelection, prepare func(string) error) (string, error) {
	version, err := s.VerifyVersion(ctx, selection)
	if err != nil {
		return "", err
	}
	return mergeVerifiedSnapshot(ctx, version.RecoverySnapshot(selection.ID), prepare)
}

// RecoverySnapshot carries only Git transaction metadata. It is not a UI diff.
func (v ReviewVersion) RecoverySnapshot(id string) Snapshot {
	h := v.Header
	return Snapshot{ID: id, Path: h.Repository, Branch: h.Branch, Target: h.Target, Head: h.Head, TargetHead: h.TargetHead, Base: h.Base, Dirty: h.Dirty, Committed: h.Committed}
}
