package localreview

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// Branches lists local target refs without inspecting file contents or a target revision.
// Callers must authorize the repository path before invoking it.
func Branches(ctx context.Context, path string) ([]string, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	root, err := trimmed(ctx, canonical, "rev-parse", "--show-toplevel")
	if err != nil {
		repositories, discoveryErr := Repositories(ctx, canonical)
		if discoveryErr != nil {
			return nil, discoveryErr
		}
		if len(repositories) == 1 {
			return Branches(ctx, repositories[0])
		}
		return nil, errors.New("select a Git repository before listing branches")
	}
	if filepath.Clean(root) != filepath.Clean(canonical) {
		return nil, errors.New("select the repository root")
	}
	refs, err := trimmed(ctx, canonical, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	if refs == "" {
		return []string{}, nil
	}
	return strings.Split(refs, "\n"), nil
}
