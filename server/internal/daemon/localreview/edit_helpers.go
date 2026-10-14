package localreview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// writeWorkingFile atomically replaces the rooted working file with content,
// preserving mode. It writes to a temp file in the same directory and renames
// it over the original, so a partial write never corrupts the file.
func writeWorkingFile(ctx context.Context, root *os.Root, path string, content []byte, mode os.FileMode) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tmp := filepath.Join(filepath.Dir(path), ".edit-tmp-"+randHex())
	out, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := out.Write(content); err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, path); err != nil {
		root.Remove(tmp)
		return err
	}
	return nil
}

// runGitMergeFile runs `git merge-file -p current base other`, returning the
// merged content on stdout and the process exit code. Exit code 1 (a real
// conflict) is returned as code, not as an error, so the caller can defer to
// the runtime. A nil error with a non-zero code means a clean merge.
func runGitMergeFile(ctx context.Context, currentPath, basePath, otherPath string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "--no-pager", "merge-file", "-p", currentPath, basePath, otherPath)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = bytes.NewBuffer(make([]byte, 0, 1024))
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out.Bytes(), exitErr.ExitCode(), nil
		}
		return nil, 0, err
	}
	return out.Bytes(), 0, nil
}

func sha256OfBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
