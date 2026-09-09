package localreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type VersionContentRequest struct {
	FilePath, Side string
	Offset         int64
	Limit          int
}

// VersionContent uses cache first, then verifies an immutable Git object or the
// complete original working-file fingerprint. New live bytes are never substituted.
func (s *BlobStore) VersionContent(ctx context.Context, version ReviewVersion, request VersionContentRequest) (ContentPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if request.Side != "old" && request.Side != "new" {
		return ContentPage{}, ErrInvalidReviewVersion
	}
	index := slices.IndexFunc(version.Files, func(file VersionFile) bool { return file.Path == request.FilePath })
	if index < 0 || !validVersionPath(request.FilePath) {
		return ContentPage{}, ErrInvalidReviewVersion
	}
	file := version.Files[index]
	if file.Preview == "unsupported" {
		return ContentPage{}, ErrFilePreviewUnavailable
	}
	reference, oid, cached, tree, name := file.New, file.NewOID, file.NewCached, version.Header.Head, file.Path
	absent := request.Side == "new" && file.Status == "deleted"
	if request.Side == "old" {
		reference, oid, cached, tree = file.Old, file.OldOID, file.OldCached, version.Header.Base
		if file.OldPath != "" {
			name = file.OldPath
		}
		absent = file.Status == "added" || file.Status == "untracked"
	}
	window := contentWindow{Offset: request.Offset, Limit: request.Limit, Binary: file.Preview == "binary"}
	if absent {
		digest := sha256.Sum256(nil)
		return streamSourceContent(ctx, strings.NewReader(""), sourceContentRead{Window: window, SHA256: hex.EncodeToString(digest[:])})
	}
	if reference != nil {
		window.Size = reference.Size
		if cached {
			page, err := s.ContentPage(ctx, CachedContentPage{Blob: *reference, Offset: request.Offset, Limit: request.Limit, Binary: window.Binary})
			if err == nil {
				return page, nil
			}
			if !errors.Is(err, os.ErrNotExist) {
				return ContentPage{}, err
			}
		}
	}
	if validGitObjectID(oid) && strings.Trim(oid, "0") != "" {
		mapped, err := trimmed(ctx, version.Header.Repository, "rev-parse", "--verify", tree+":"+name)
		if err != nil || mapped != oid {
			return ContentPage{}, ErrSnapshotContentChanged
		}
		rawSize, err := trimmed(ctx, version.Header.Repository, "cat-file", "-s", oid)
		if err != nil {
			return ContentPage{}, err
		}
		size, err := strconv.ParseInt(rawSize, 10, 64)
		if err != nil || size < 0 || size > 1<<40 {
			return ContentPage{}, ErrInvalidSnapshotBlob
		}
		window.Size = size
		expected := ""
		if reference != nil {
			if size != reference.Size {
				return ContentPage{}, ErrSnapshotContentChanged
			}
			expected = reference.ID
		}
		var page ContentPage
		err = readGitObject(ctx, gitObjectRequest{Repository: version.Header.Repository, OID: oid}, func(reader io.Reader) error {
			var err error
			page, err = streamSourceContent(ctx, reader, sourceContentRead{Window: window, SHA256: expected, GitOID: oid})
			return err
		})
		if err != nil {
			return ContentPage{}, err
		}
		return page, nil
	}
	if reference == nil || request.Side != "new" || version.Header.Committed {
		return ContentPage{}, ErrFilePreviewUnavailable
	}
	root, err := os.OpenRoot(version.Header.Repository)
	if err != nil {
		return ContentPage{}, err
	}
	defer root.Close()
	path := filepath.FromSlash(file.Path)
	info, err := root.Lstat(path)
	if err != nil {
		return ContentPage{}, err
	}
	identity := sourceContentRead{Window: window, SHA256: reference.ID}
	if file.NewMode == "120000" {
		if info.Mode()&os.ModeSymlink == 0 {
			return ContentPage{}, ErrSnapshotContentChanged
		}
		text, err := root.Readlink(path)
		if err != nil {
			return ContentPage{}, err
		}
		return streamSourceContent(ctx, strings.NewReader(text), identity)
	}
	if !info.Mode().IsRegular() || info.Size() != reference.Size {
		return ContentPage{}, ErrSnapshotContentChanged
	}
	input, err := root.Open(path)
	if err != nil {
		return ContentPage{}, err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil {
		return ContentPage{}, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return ContentPage{}, ErrSnapshotContentChanged
	}
	return streamSourceContent(ctx, input, identity)
}
