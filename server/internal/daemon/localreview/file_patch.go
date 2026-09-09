package localreview

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var ErrFilePreviewUnavailable = errors.New("this file requires content view instead of a diff preview")

// FilePatch generates only the requested file's diff from verified captured bytes.
// No source checkout, index, external filter or user diff helper is consulted.
func (s *BlobStore) FilePatch(ctx context.Context, file VersionFile) (BlobRef, error) {
	if file.Preview != "text" {
		return BlobRef{}, ErrFilePreviewUnavailable
	}
	if file.Old == nil && file.New == nil {
		return BlobRef{}, ErrInvalidReviewVersion
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	key := s.patchKey(file)
	if blob, found := generatedPatches.get(key); found {
		err := s.consume(ctx, blob, func(reader io.Reader) error {
			_, err := io.Copy(io.Discard, reader)
			return err
		})
		if err == nil {
			return blob, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return BlobRef{}, err
		}
	}
	directory, err := os.MkdirTemp("", "multica-review-patch-")
	if err != nil {
		return BlobRef{}, err
	}
	defer os.RemoveAll(directory)
	for _, side := range []struct {
		name, mode string
		blob       *BlobRef
	}{{"old", file.OldMode, file.Old}, {"new", file.NewMode, file.New}} {
		permissions := os.FileMode(0600)
		if side.mode == "100755" {
			permissions = 0700
		}
		output, err := os.OpenFile(filepath.Join(directory, side.name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, permissions)
		if err != nil {
			return BlobRef{}, err
		}
		var copyErr error
		if side.blob != nil {
			copyErr = s.consume(ctx, *side.blob, func(reader io.Reader) error {
				_, err := io.CopyBuffer(output, reader, make([]byte, 64<<10))
				return err
			})
		}
		closeErr := output.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return BlobRef{}, err
		}
	}
	cmd := exec.CommandContext(ctx, "git", "--no-pager", "-c", "core.hooksPath="+os.DevNull, "-c", "core.fsmonitor=false", "-C", directory, "diff", "--no-index", "--no-ext-diff", "--no-textconv", "--no-renames", "--src-prefix=a/", "--dst-prefix=b/", "--", "old", "new")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return BlobRef{}, err
	}
	if err := cmd.Start(); err != nil {
		stdout.Close()
		return BlobRef{}, err
	}
	blob, readErr := s.Put(ctx, stdout)
	if readErr != nil {
		cancel()
	}
	stdout.Close()
	waitErr := cmd.Wait()
	if readErr != nil {
		return BlobRef{}, readErr
	}
	if waitErr != nil {
		var exit *exec.ExitError
		if !errors.As(waitErr, &exit) || exit.ExitCode() != 1 {
			return BlobRef{}, waitErr
		}
	}
	generatedPatches.put(key, blob)
	return blob, nil
}
