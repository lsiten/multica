package localreview

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const maxVersionBytes = 32 << 20
const maxVersionFiles = 10000

var ErrInvalidReviewVersion = errors.New("invalid local review version")

type VersionHeader struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Target     string `json:"target"`
	Head       string `json:"head"`
	TargetHead string `json:"target_head"`
	Base       string `json:"base"`
	Dirty      bool   `json:"dirty"`
	Committed  bool   `json:"committed"`
}

// VersionFile is immutable metadata. Patch bytes are generated/read separately.
type VersionFile struct {
	Path      string   `json:"path"`
	OldPath   string   `json:"old_path,omitempty"`
	Status    string   `json:"status"`
	OldMode   string   `json:"old_mode,omitempty"`
	NewMode   string   `json:"new_mode,omitempty"`
	Old       *BlobRef `json:"old,omitempty"`
	New       *BlobRef `json:"new,omitempty"`
	OldCached bool     `json:"old_cached"`
	NewCached bool     `json:"new_cached"`
	OldOID    string   `json:"old_oid,omitempty"`
	NewOID    string   `json:"new_oid,omitempty"`
	Preview   string   `json:"preview"`
	Additions int64    `json:"additions"`
	Deletions int64    `json:"deletions"`
}

type ReviewVersion struct {
	Format int           `json:"format"`
	Header VersionHeader `json:"header"`
	Files  []VersionFile `json:"files"`
}

// SaveVersion canonicalizes metadata so listing order cannot invalidate approval.
// Source and target identities and every captured file digest participate in the ID.
func (s *BlobStore) SaveVersion(ctx context.Context, version ReviewVersion) (string, error) {
	version.Format = 2
	version.Files = slices.Clone(version.Files)
	slices.SortFunc(version.Files, func(a, b VersionFile) int { return strings.Compare(a.Path, b.Path) })
	if err := validateReviewVersion(version); err != nil {
		return "", err
	}
	data, err := json.Marshal(version)
	if err != nil {
		return "", err
	}
	if len(data) > maxVersionBytes {
		return "", ErrInvalidReviewVersion
	}
	blob, err := s.put(ctx, bytes.NewReader(data), true)
	return blob.ID, err
}

func (s *BlobStore) LoadVersion(ctx context.Context, id string) (ReviewVersion, error) {
	var version ReviewVersion
	if !validBlobID(id) {
		return version, ErrInvalidReviewVersion
	}
	marker, err := s.catalog.Lstat(id)
	if err != nil {
		return version, err
	}
	if !marker.Mode().IsRegular() {
		return version, os.ErrInvalid
	}
	info, err := s.root.Lstat(id)
	if err != nil {
		return version, err
	}
	if info.Size() > maxVersionBytes {
		return version, ErrInvalidReviewVersion
	}
	err = s.consume(ctx, BlobRef{ID: id, Size: info.Size()}, func(reader io.Reader) error {
		decoder := json.NewDecoder(reader)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&version); err != nil {
			return err
		}
		if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
			return ErrInvalidReviewVersion
		}
		return validateReviewVersion(version)
	})
	if err != nil {
		return ReviewVersion{}, err
	}
	return version, nil
}

func validateReviewVersion(version ReviewVersion) error {
	h := version.Header
	if version.Format != 2 || !filepath.IsAbs(h.Repository) || h.Branch == "" || h.Target == "" || !validGitObjectID(h.Head) || !validGitObjectID(h.TargetHead) || !validGitObjectID(h.Base) || len(version.Files) > maxVersionFiles {
		return ErrInvalidReviewVersion
	}
	previous := ""
	for _, file := range version.Files {
		if !validVersionPath(file.Path) || file.Path <= previous || (file.OldPath != "" && !validVersionPath(file.OldPath)) || file.Additions < 0 || file.Deletions < 0 {
			return ErrInvalidReviewVersion
		}
		previous = file.Path
		switch file.Status {
		case "added", "untracked":
			if file.Old != nil || (file.New == nil && !validGitObjectID(file.NewOID)) {
				return ErrInvalidReviewVersion
			}
		case "deleted":
			if (file.Old == nil && !validGitObjectID(file.OldOID)) || file.New != nil {
				return ErrInvalidReviewVersion
			}
		case "modified", "renamed", "type_changed":
			if (file.Old == nil && !validGitObjectID(file.OldOID)) || (file.New == nil && !validGitObjectID(file.NewOID)) {
				return ErrInvalidReviewVersion
			}
		default:
			return ErrInvalidReviewVersion
		}
		switch file.Preview {
		case "text", "binary", "too_large", "uncached", "unsupported":
		default:
			return ErrInvalidReviewVersion
		}
		for _, blob := range []*BlobRef{file.Old, file.New} {
			if blob != nil && (!validBlobID(blob.ID) || blob.Size < 0) {
				return ErrInvalidReviewVersion
			}
		}
	}
	return nil
}

func validVersionPath(name string) bool {
	return name != "" && len(name) <= 4096 && !strings.ContainsRune(name, 0) && filepath.IsLocal(filepath.FromSlash(name)) && filepath.ToSlash(filepath.Clean(filepath.FromSlash(name))) == name && name != "."
}

func validGitObjectID(id string) bool {
	if len(id) != 40 && len(id) != 64 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

type FilePageRequest struct{ Offset, Limit int }
type VersionFilePage struct {
	Files      []VersionFile `json:"files"`
	TotalFiles int           `json:"total_files"`
	NextOffset int           `json:"next_offset"`
	HasMore    bool          `json:"has_more"`
	Additions  int64         `json:"additions"`
	Deletions  int64         `json:"deletions"`
}

func (v ReviewVersion) FilePage(request FilePageRequest) (VersionFilePage, error) {
	page := VersionFilePage{Files: []VersionFile{}, TotalFiles: len(v.Files)}
	if request.Offset < 0 || request.Offset > len(v.Files) || request.Limit < 1 || request.Limit > 200 {
		return page, ErrInvalidReviewVersion
	}
	end := min(request.Offset+request.Limit, len(v.Files))
	page.Files = append(page.Files, v.Files[request.Offset:end]...)
	page.NextOffset, page.HasMore = end, end < len(v.Files)
	for _, file := range v.Files {
		page.Additions += file.Additions
		page.Deletions += file.Deletions
	}
	return page, nil
}
