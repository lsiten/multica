package localreview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *BlobStore) captureWorkingFile(ctx context.Context, root *os.Root, file *VersionFile) error {
	name := filepath.FromSlash(file.Path)
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	var captured CapturedContent
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := root.Readlink(name)
		if err != nil {
			return err
		}
		captured, err = s.CaptureContent(ctx, strings.NewReader(target))
		if err != nil {
			return err
		}
		file.NewMode = "120000"
	case info.Mode().IsRegular():
		input, err := root.Open(name)
		if err != nil {
			return err
		}
		opened, statErr := input.Stat()
		if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			input.Close()
			return ErrSnapshotContentChanged
		}
		captured, err = s.CaptureContent(ctx, input)
		after, afterErr := input.Stat()
		closeErr := input.Close()
		if err := errors.Join(err, afterErr, closeErr); err != nil {
			return err
		}
		if after.Size() != opened.Size() || after.ModTime() != opened.ModTime() || after.Mode() != opened.Mode() {
			return ErrSnapshotContentChanged
		}
		if file.NewMode == "" {
			file.NewMode = "100644"
			if opened.Mode()&0111 != 0 {
				file.NewMode = "100755"
			}
		}
	default:
		// Special files and nested repositories are metadata-only, never opened as streams.
		descriptor := fmt.Sprintf("%s:%d:%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		captured, err = s.CaptureContent(ctx, strings.NewReader(descriptor))
		if err != nil {
			return err
		}
		file.Preview = "unsupported"
		file.NewMode = info.Mode().String()
	}
	file.New = &captured.Blob
	file.NewCached = captured.Cached
	file.NewOID = ""
	if file.Preview == "unsupported" {
		return nil
	}
	if captured.CacheLimited {
		file.Preview = "uncached"
	} else if !captured.Cached {
		file.Preview = "too_large"
	} else if captured.Binary && file.Preview == "text" {
		file.Preview = "binary"
	}
	if file.Status == "untracked" && !captured.Binary && file.Preview != "unsupported" {
		file.Additions = captured.Lines
	}
	return nil
}
