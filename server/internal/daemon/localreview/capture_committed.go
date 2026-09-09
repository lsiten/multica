package localreview

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// CaptureCommitted freezes a pinned Git comparison without producing patch bodies.
// The caller must authorize the repository and the task-owned cache first.
func (s *BlobStore) CaptureCommitted(ctx context.Context, request CommittedRequest) (ReviewVersion, error) {
	version := ReviewVersion{Format: 2, Files: []VersionFile{}}
	header, err := captureVersionHeader(ctx, request)
	if err != nil {
		return version, err
	}
	version.Header = header
	repository, base, head := header.Repository, header.Base, header.Head
	raw, err := git(ctx, repository, "diff", "--raw", "-z", "--no-renames", "--no-abbrev", "--no-ext-diff", "--no-textconv", base, head, "--")
	if err != nil {
		return version, err
	}
	files, err := parseRawVersionFiles(raw)
	if err != nil {
		return version, err
	}
	stats, err := git(ctx, repository, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", base, head, "--")
	if err != nil {
		return version, err
	}
	if err := applyVersionStats(files, stats); err != nil {
		return version, err
	}
	for i := range files {
		file := &files[i]
		for _, side := range []struct {
			oid, mode   string
			destination **BlobRef
			cached      *bool
		}{{file.OldOID, file.OldMode, &file.Old, &file.OldCached}, {file.NewOID, file.NewMode, &file.New, &file.NewCached}} {
			if side.mode == "000000" {
				continue
			}
			if side.mode == "160000" {
				file.Preview = "unsupported"
				continue
			}
			blob, binary, err := s.captureObject(ctx, gitObjectRequest{Repository: repository, OID: side.oid})
			if errors.Is(err, ErrSnapshotBlobTooLarge) || errors.Is(err, ErrSnapshotCacheFull) {
				if file.Preview != "unsupported" {
					file.Preview = "too_large"
					if errors.Is(err, ErrSnapshotCacheFull) {
						file.Preview = "uncached"
					}
				}
				continue
			}
			if err != nil {
				return version, err
			}
			*side.destination = &blob
			*side.cached = true
			if binary && file.Preview == "text" {
				file.Preview = "binary"
			}
		}
	}
	version.Files = files
	if err := validateReviewVersion(version); err != nil {
		return ReviewVersion{}, err
	}
	return version, nil
}

func parseRawVersionFiles(raw string) ([]VersionFile, error) {
	files := []VersionFile{}
	parts := strings.Split(raw, "\x00")
	for i := 0; i < len(parts)-1; i += 2 {
		if i+1 >= len(parts) || !strings.HasPrefix(parts[i], ":") {
			return nil, ErrInvalidReviewVersion
		}
		fields := strings.Fields(parts[i][1:])
		if len(fields) != 5 || !validVersionPath(parts[i+1]) {
			return nil, ErrInvalidReviewVersion
		}
		file := VersionFile{Path: parts[i+1], OldMode: fields[0], NewMode: fields[1], OldOID: fields[2], NewOID: fields[3], Preview: "text"}
		switch fields[4] {
		case "A":
			file.Status = "added"
		case "D":
			file.Status = "deleted"
		case "M":
			file.Status = "modified"
		case "T":
			file.Status = "type_changed"
		default:
			return nil, ErrInvalidReviewVersion
		}
		files = append(files, file)
		if len(files) > maxVersionFiles {
			return nil, ErrInvalidReviewVersion
		}
	}
	return files, nil
}

func applyVersionStats(files []VersionFile, raw string) error {
	byPath := map[string]int{}
	for i, file := range files {
		byPath[file.Path] = i
	}
	for _, entry := range strings.Split(raw, "\x00") {
		if entry == "" {
			continue
		}
		fields := strings.SplitN(entry, "\t", 3)
		if len(fields) != 3 {
			return ErrInvalidReviewVersion
		}
		i, ok := byPath[fields[2]]
		if !ok {
			return ErrInvalidReviewVersion
		}
		if fields[0] == "-" && fields[1] == "-" {
			files[i].Preview = "binary"
			continue
		}
		added, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || added < 0 {
			return ErrInvalidReviewVersion
		}
		removed, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || removed < 0 {
			return ErrInvalidReviewVersion
		}
		files[i].Additions, files[i].Deletions = added, removed
	}
	return nil
}
