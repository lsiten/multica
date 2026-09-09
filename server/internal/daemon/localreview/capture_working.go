package localreview

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"time"
)

type WorkingVersionRequest struct{ Path, Target string }

// CaptureWorking pins the raw bytes the reviewer sees, including unstaged and
// untracked files. It does not run clean filters or modify the user's index.
func (s *BlobStore) CaptureWorking(ctx context.Context, request WorkingVersionRequest) (ReviewVersion, error) {
	version := ReviewVersion{Format: 2, Files: []VersionFile{}}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	head, err := trimmed(ctx, request.Path, "rev-parse", "HEAD")
	if err != nil {
		return version, err
	}
	branch, err := trimmed(ctx, request.Path, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return version, errors.New("select a local source branch before reviewing")
	}
	header, err := captureVersionHeader(ctx, CommittedRequest{Path: request.Path, Branch: branch, Head: head, Target: request.Target})
	if err != nil {
		return version, err
	}
	header.Committed = false
	repository := header.Repository
	status, err := git(ctx, repository, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return version, err
	}
	header.Dirty = status != ""
	raw, err := git(ctx, repository, "diff", "--raw", "-z", "--no-renames", "--no-abbrev", "--no-ext-diff", "--no-textconv", header.Base, "--")
	if err != nil {
		return version, err
	}
	files, err := parseRawVersionFiles(raw)
	if err != nil {
		return version, err
	}
	stats, err := git(ctx, repository, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", header.Base, "--")
	if err != nil {
		return version, err
	}
	if err := applyVersionStats(files, stats); err != nil {
		return version, err
	}
	untracked, err := git(ctx, repository, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return version, err
	}
	for _, name := range strings.Split(untracked, "\x00") {
		if name == "" {
			continue
		}
		name = strings.TrimSuffix(name, "/")
		if !validVersionPath(name) {
			return version, ErrInvalidReviewVersion
		}
		files = append(files, VersionFile{Path: name, Status: "untracked", OldMode: "000000", Preview: "text"})
	}
	if len(files) > maxVersionFiles {
		return version, ErrInvalidReviewVersion
	}
	root, err := os.OpenRoot(repository)
	if err != nil {
		return version, err
	}
	defer root.Close()
	for i := range files {
		file := &files[i]
		if file.OldMode != "000000" {
			if file.OldMode == "160000" {
				file.Preview = "unsupported"
			} else {
				blob, binary, err := s.captureObject(ctx, gitObjectRequest{Repository: repository, OID: file.OldOID})
				if errors.Is(err, ErrSnapshotBlobTooLarge) || errors.Is(err, ErrSnapshotCacheFull) {
					file.Preview = "too_large"
					if errors.Is(err, ErrSnapshotCacheFull) {
						file.Preview = "uncached"
					}
				} else if err != nil {
					return version, err
				} else {
					file.Old = &blob
					file.OldCached = true
					if binary {
						file.Preview = "binary"
					}
				}
			}
		}
		if file.NewMode != "000000" {
			if err := s.captureWorkingFile(ctx, root, file); err != nil {
				return version, err
			}
		}
	}
	after, err := git(ctx, repository, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return version, err
	}
	currentHead, err := trimmed(ctx, repository, "rev-parse", "HEAD")
	if err != nil {
		return version, err
	}
	currentTarget, err := trimmed(ctx, repository, "rev-parse", "refs/heads/"+header.Target)
	if err != nil {
		return version, err
	}
	if after != status || currentHead != header.Head || currentTarget != header.TargetHead {
		return version, ErrSnapshotContentChanged
	}
	slices.SortFunc(files, func(a, b VersionFile) int { return strings.Compare(a.Path, b.Path) })
	version.Header, version.Files = header, files
	if err := validateReviewVersion(version); err != nil {
		return ReviewVersion{}, err
	}
	return version, nil
}
