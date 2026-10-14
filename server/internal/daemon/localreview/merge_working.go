package localreview

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ErrWorkingFileConflict means the edited change could not be merged against the
// current working file; the runtime version was kept and the caller must rebase
// against the current content.
var ErrWorkingFileConflict = errors.New("edit could not be merged; the runtime version was kept")

// MergeWorkingFileRequest is a three-way merge of an edited file against its
// current content. Base is the content the editor opened, New is the edited
// content, and the working file's current content is read from the repository.
type MergeWorkingFileRequest struct {
	FilePath string
	Base     []byte
	New      []byte
}

type MergeWorkingFileResult struct {
	FilePath       string
	Merged         bool
	Conflict       bool
	NewContent     []byte
	PreviousSHA256 string
	CurrentSHA256  string
	Bytes          int64
}

// MergeWorkingFile merges the editor's change (Base -> New) into the working
// file's current content using a three-way merge. A clean merge is written
// (atomically, preserving mode); an unresolvable conflict keeps the runtime
// version and reports Conflict so the caller defers to the runtime. It never
// touches the git index.
func MergeWorkingFile(ctx context.Context, root *os.Root, request MergeWorkingFileRequest) (MergeWorkingFileResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !validVersionPath(request.FilePath) {
		return MergeWorkingFileResult{}, ErrInvalidReviewVersion
	}
	path := filepath.FromSlash(request.FilePath)
	info, err := root.Lstat(path)
	if err != nil {
		return MergeWorkingFileResult{}, err
	}
	if !info.Mode().IsRegular() {
		return MergeWorkingFileResult{}, ErrWorkingFileChanged
	}
	current, err := readWorkingFile(ctx, root, path)
	if err != nil {
		return MergeWorkingFileResult{}, err
	}
	currentDigest := sha256OfBytes(current)
	// Fast path: the file is unchanged since the editor opened it, so the edited
	// content is the whole new content (no merge needed).
	if currentDigest == sha256OfBytes(request.Base) {
		if err := writeWorkingFile(ctx, root, path, request.New, info.Mode().Perm()); err != nil {
			return MergeWorkingFileResult{}, err
		}
		return MergeWorkingFileResult{
			FilePath:       request.FilePath,
			Merged:         true,
			NewContent:     request.New,
			PreviousSHA256: currentDigest,
			CurrentSHA256:  sha256OfBytes(request.New),
			Bytes:          int64(len(request.New)),
		}, nil
	}
	merged, conflict, err := threeWayMerge(ctx, current, request.Base, request.New)
	if err != nil {
		return MergeWorkingFileResult{}, err
	}
	if conflict {
		return MergeWorkingFileResult{
			FilePath:       request.FilePath,
			Conflict:       true,
			NewContent:     current,
			PreviousSHA256: currentDigest,
			CurrentSHA256:  currentDigest,
		}, nil
	}
	if err := writeWorkingFile(ctx, root, path, merged, info.Mode().Perm()); err != nil {
		return MergeWorkingFileResult{}, err
	}
	return MergeWorkingFileResult{
		FilePath:       request.FilePath,
		Merged:         true,
		NewContent:     merged,
		PreviousSHA256: currentDigest,
		CurrentSHA256:  sha256OfBytes(merged),
		Bytes:          int64(len(merged)),
	}, nil
}

func readWorkingFile(ctx context.Context, root *os.Root, path string) ([]byte, error) {
	input, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() {
		return nil, ErrWorkingFileChanged
	}
	return io.ReadAll(io.LimitReader(reviewContextReader{ctx, input}, 1<<40+1))
}

// threeWayMerge merges other into current using base as the common ancestor.
// It returns the merged content and conflict=true when the change cannot be
// cleanly applied to current.
func threeWayMerge(ctx context.Context, current, base, other []byte) (merged []byte, conflict bool, err error) {
	tmp, err := os.MkdirTemp("", "multica-3way-")
	if err != nil {
		return nil, false, err
	}
	defer os.RemoveAll(tmp)
	currentPath, basePath, otherPath := filepath.Join(tmp, "current"), filepath.Join(tmp, "base"), filepath.Join(tmp, "other")
	for name, content := range map[string][]byte{currentPath: current, basePath: base, otherPath: other} {
		if err := os.WriteFile(name, content, 0600); err != nil {
			return nil, false, err
		}
	}
	out, code, e := runGitMergeFile(ctx, currentPath, basePath, otherPath)
	if e != nil {
		return nil, false, e
	}
	if code == 1 {
		return current, true, nil
	}
	if code != 0 {
		return nil, false, errors.New("git merge-file failed")
	}
	return out, false, nil
}
