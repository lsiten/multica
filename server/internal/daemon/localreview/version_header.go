package localreview

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
)

func captureVersionHeader(ctx context.Context, request CommittedRequest) (VersionHeader, error) {
	header := VersionHeader{}
	if !validGitObjectID(request.Head) {
		return header, ErrInvalidReviewVersion
	}
	repository, err := filepath.EvalSymlinks(request.Path)
	if err != nil {
		return header, err
	}
	root, err := trimmed(ctx, repository, "rev-parse", "--show-toplevel")
	if err != nil {
		return header, err
	}
	if root != repository {
		return header, errors.New("select the repository root")
	}
	branches, err := Branches(ctx, repository)
	if err != nil {
		return header, err
	}
	if !slices.Contains(branches, request.Target) {
		return header, errors.New("target must be an existing local branch")
	}
	head, err := trimmed(ctx, repository, "rev-parse", "--verify", request.Head+"^{commit}")
	if err != nil {
		return header, err
	}
	target, err := trimmed(ctx, repository, "rev-parse", "refs/heads/"+request.Target)
	if err != nil {
		return header, err
	}
	base, err := trimmed(ctx, repository, "merge-base", head, target)
	if err != nil {
		return header, err
	}
	return VersionHeader{Repository: repository, Branch: request.Branch, Target: request.Target, Head: head, TargetHead: target, Base: base, Committed: true}, nil
}
