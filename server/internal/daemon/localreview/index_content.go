package localreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (s *BlobStore) stagingContent(ctx context.Context, repository string, file VersionFile, consume func(io.Reader) error) error {
	if file.New == nil {
		return ErrInvalidSnapshotBlob
	}
	if file.NewCached {
		err := s.consume(ctx, *file.New, consume)
		if err == nil || !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if !validVersionPath(file.Path) {
		return ErrInvalidReviewVersion
	}
	root, err := os.OpenRoot(repository)
	if err != nil {
		return err
	}
	defer root.Close()
	name := filepath.FromSlash(file.Path)
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if file.NewMode == "120000" {
		if info.Mode()&os.ModeSymlink == 0 {
			return ErrSnapshotContentChanged
		}
		text, err := root.Readlink(name)
		if err != nil {
			return err
		}
		return consumeWorkingFingerprint(ctx, strings.NewReader(text), *file.New, consume)
	}
	if !info.Mode().IsRegular() || info.Size() != file.New.Size || (info.Mode()&0111 != 0) != (file.NewMode == "100755") {
		return ErrSnapshotContentChanged
	}
	input, err := root.Open(name)
	if err != nil {
		return err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return ErrSnapshotContentChanged
	}
	return consumeWorkingFingerprint(ctx, input, *file.New, consume)
}

// A consumer can prepare an object while streaming, but must not publish an
// index/ref until this function verifies every byte against the shown snapshot.
func consumeWorkingFingerprint(ctx context.Context, input io.Reader, expected BlobRef, consume func(io.Reader) error) error {
	if !validBlobID(expected.ID) || expected.Size < 0 || expected.Size > 1<<40 {
		return ErrInvalidSnapshotBlob
	}
	hash := sha256.New()
	probe := &contentProbe{reader: io.TeeReader(io.LimitReader(reviewContextReader{ctx, input}, expected.Size+1), hash)}
	if err := consume(probe); err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, probe); err != nil {
		return err
	}
	if probe.size != expected.Size || hex.EncodeToString(hash.Sum(nil)) != expected.ID {
		return ErrSnapshotContentChanged
	}
	return ctx.Err()
}
