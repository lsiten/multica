package localreview

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

var ErrWorkingFileChanged = errors.New("working file changed since the base; reload before editing")

// EditWorkingFileRequest is a compare-and-swap write of edited content to a
// working file. BaseSHA256 is the digest of the content the editor opened; the
// write is only performed when the working file still equals that digest, so a
// concurrent change (agent or another editor) is never silently overwritten.
type EditWorkingFileRequest struct {
	FilePath   string
	BaseSHA256 string
	NewContent []byte
}

// EditWorkingFileResult reports the applied edit. PreviousSHA256 is the base the
// write was verified against; NewSHA256 is the digest after the write.
type EditWorkingFileResult struct {
	FilePath       string
	PreviousSHA256 string
	NewSHA256      string
	Bytes          int64
}

// ApplyWorkingFileEdit writes NewContent to the rooted working file only when the
// file still equals BaseSHA256. It never touches the git index, writes only
// regular files inside the rooted repository, and is atomic: content is written
// to a temp file in the same directory and renamed over the original, preserving
// the original mode. A changed base yields ErrWorkingFileChanged so the caller
// can rebase against the current content.
func ApplyWorkingFileEdit(ctx context.Context, root *os.Root, request EditWorkingFileRequest) (EditWorkingFileResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !validVersionPath(request.FilePath) {
		return EditWorkingFileResult{}, ErrInvalidReviewVersion
	}
	if len(request.BaseSHA256) != 64 {
		return EditWorkingFileResult{}, ErrInvalidSnapshotBlob
	}
	path := filepath.FromSlash(request.FilePath)
	info, err := root.Lstat(path)
	if err != nil {
		return EditWorkingFileResult{}, err
	}
	if !info.Mode().IsRegular() {
		return EditWorkingFileResult{}, ErrWorkingFileChanged
	}
	input, err := root.Open(path)
	if err != nil {
		return EditWorkingFileResult{}, err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil {
		return EditWorkingFileResult{}, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return EditWorkingFileResult{}, ErrWorkingFileChanged
	}
	current, err := hashWorkingFile(ctx, input)
	if err != nil {
		return EditWorkingFileResult{}, err
	}
	if current != request.BaseSHA256 {
		return EditWorkingFileResult{}, ErrWorkingFileChanged
	}
	tmp := filepath.Join(filepath.Dir(path), ".edit-tmp-"+randHex())
	out, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return EditWorkingFileResult{}, err
	}
	written, err := out.Write(request.NewContent)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		root.Remove(tmp)
		return EditWorkingFileResult{}, err
	}
	newHash, err := hashBytes(request.NewContent)
	if err != nil {
		root.Remove(tmp)
		return EditWorkingFileResult{}, err
	}
	if err := root.Rename(tmp, path); err != nil {
		root.Remove(tmp)
		return EditWorkingFileResult{}, err
	}
	return EditWorkingFileResult{
		FilePath:       request.FilePath,
		PreviousSHA256: current,
		NewSHA256:      newHash,
		Bytes:          int64(written),
	}, nil
}

func hashWorkingFile(ctx context.Context, input io.Reader) (string, error) {
	hasher := sha256.New()
	if _, err := io.Copy(hasher, io.LimitReader(reviewContextReader{ctx, input}, 1<<40+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func hashBytes(content []byte) (string, error) {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}

func randHex() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

