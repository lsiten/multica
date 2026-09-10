package localreview

import (
	"context"
	"errors"
	"os"
)

type workingPathComparison struct{ Repository, Base, Path string }

// A staged deletion hides a recreated untracked path from ordinary git diff.
// Compare that path using a private base-tree index, never the real index.
func compareRecreatedPath(ctx context.Context, request workingPathComparison) (*VersionFile, error) {
	directory, err := os.MkdirTemp("", "multica-review-net-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	index := &indexTransaction{root: root, scratch: "index", repository: request.Repository}
	if _, err := index.git(ctx, nil, "read-tree", request.Base); err != nil {
		return nil, err
	}
	filters, err := InspectionFilterOptions(ctx, request.Repository)
	if err != nil {
		return nil, err
	}
	rawArgs := append(append([]string{}, filters...), "diff", "--raw", "-z", "--no-abbrev", "--no-renames", "--no-ext-diff", "--no-textconv", request.Base, "--", request.Path)
	raw, err := index.git(ctx, nil, rawArgs...)
	if err != nil {
		return nil, err
	}
	files, err := parseRawVersionFiles(raw)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	if len(files) != 1 || files[0].Path != request.Path {
		return nil, ErrInvalidReviewVersion
	}
	statsArgs := append(append([]string{}, filters...), "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", request.Base, "--", request.Path)
	stats, err := index.git(ctx, nil, statsArgs...)
	if err != nil {
		return nil, err
	}
	if err := applyVersionStats(files, stats); err != nil {
		return nil, err
	}
	if files[0].NewMode == "000000" {
		return nil, errors.Join(ErrSnapshotContentChanged, ErrInvalidReviewVersion)
	}
	return &files[0], nil
}
