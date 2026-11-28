package localreview

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

type CheckoutRequest struct {
	Path   string
	Scope  string
	Target string
	Commit string
}

// CheckoutReferences lists only refs already present; it never contacts a remote.
func CheckoutReferences(ctx context.Context, path string) ([]string, error) {
	raw, err := trimmed(ctx, path, "for-each-ref", "--format=%(refname)", "refs/heads/", "refs/remotes/")
	if err != nil {
		return nil, err
	}
	refs := []string{}
	for _, ref := range strings.Split(raw, "\n") {
		if ref != "" && !strings.HasSuffix(ref, "/HEAD") {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func (s *BlobStore) CaptureCheckout(ctx context.Context, request CheckoutRequest) (ReviewVersion, error) {
	path, err := filepath.EvalSymlinks(request.Path)
	if err != nil {
		return ReviewVersion{}, err
	}
	root, err := trimmed(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil || root != path {
		return ReviewVersion{}, errors.New("select the repository root")
	}
	head, err := trimmed(ctx, path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return ReviewVersion{}, err
	}
	branch, err := trimmed(ctx, path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ReviewVersion{}, err
	}
	header := VersionHeader{Repository: path, Branch: branch, Head: head, Committed: true, Target: request.Target}
	switch request.Scope {
	case "branch":
		refs, err := CheckoutReferences(ctx, path)
		if err != nil {
			return ReviewVersion{}, err
		}
		target := request.Target
		if !slices.Contains(refs, target) {
			if slices.Contains(refs, "refs/heads/"+target) {
				target = "refs/heads/" + target
			} else if slices.Contains(refs, "refs/remotes/"+target) {
				target = "refs/remotes/" + target
			} else {
				return ReviewVersion{}, errors.New("target must be an existing checkout ref")
			}
		}
		header.TargetHead, err = trimmed(ctx, path, "rev-parse", "--verify", target+"^{commit}")
		if err != nil {
			return ReviewVersion{}, err
		}
		header.Base, err = trimmed(ctx, path, "merge-base", head, header.TargetHead)
		if err != nil {
			return ReviewVersion{}, err
		}
	case "commit":
		if !validGitObjectID(request.Commit) {
			return ReviewVersion{}, errors.New("a full commit SHA is required")
		}
		if _, err := git(ctx, path, "merge-base", "--is-ancestor", request.Commit, head); err != nil {
			return ReviewVersion{}, errors.New("commit is outside checkout history")
		}
		parents, err := trimmed(ctx, path, "rev-list", "--parents", "-n", "1", request.Commit)
		if err != nil {
			return ReviewVersion{}, err
		}
		fields := strings.Fields(parents)
		header.Head, header.CommitOnly = request.Commit, true
		if len(fields) > 1 {
			header.Base = fields[1]
		} else {
			header.Base, err = trimmed(ctx, path, "hash-object", "-t", "tree", "--stdin")
			if err != nil {
				return ReviewVersion{}, err
			}
		}
		header.TargetHead = header.Head
	default:
		return ReviewVersion{}, errors.New("invalid Git comparison scope")
	}
	return s.captureCommittedHeader(ctx, header)
}
