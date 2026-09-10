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
)

var ErrIndexBusy = errors.New("Git index is locked by another operation")

type indexTransaction struct {
	root                                        *os.Root
	lock                                        *os.File
	lockInfo                                    os.FileInfo
	repository, branch, head, original, scratch string
	published                                   bool
}

func openIndexTransaction(ctx context.Context, repository string) (*indexTransaction, error) {
	branch, err := trimmed(ctx, repository, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return nil, err
	}
	head, err := trimmed(ctx, repository, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return nil, err
	}
	directory, err := trimmed(ctx, repository, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	lock, err := root.OpenFile("index.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		root.Close()
		return nil, errors.Join(ErrIndexBusy, err)
	}
	tx := &indexTransaction{root: root, lock: lock, repository: repository, branch: branch, head: head, scratch: ".review-index-" + rand.Text()}
	tx.lockInfo, err = lock.Stat()
	if err != nil {
		tx.Close()
		return nil, err
	}
	output, err := root.OpenFile(tx.scratch, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		tx.Close()
		return nil, err
	}
	tx.original, err = copyIndex(ctx, root, "index", output)
	err = errors.Join(err, output.Sync(), output.Close())
	if err != nil {
		tx.Close()
		return nil, err
	}
	return tx, nil
}

func (tx *indexTransaction) scratchPath() string { return filepath.Join(tx.root.Name(), tx.scratch) }

// publish holds Git's conventional index.lock throughout preparation. It only
// replaces the real index after checking the original bytes and current branch.
func (tx *indexTransaction) publish(ctx context.Context) error {
	if tx.root == nil || tx.lock == nil || tx.published {
		return ErrIndexStateChanged
	}
	if !tx.ownsLock() {
		return ErrIndexBusy
	}
	if _, err := copyIndex(ctx, tx.root, tx.scratch, tx.lock); err != nil {
		return err
	}
	if err := tx.validate(ctx); err != nil {
		return err
	}
	if err := tx.lock.Sync(); err != nil {
		return err
	}
	if err := tx.lock.Close(); err != nil {
		return err
	}
	tx.lock = nil
	if err := tx.root.Rename("index.lock", "index"); err != nil {
		return err
	}
	tx.published = true
	return nil
}

func (tx *indexTransaction) validate(ctx context.Context) error {
	branch, err := trimmed(ctx, tx.repository, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return err
	}
	head, err := trimmed(ctx, tx.repository, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	original, err := copyIndex(ctx, tx.root, "index", io.Discard)
	if err != nil {
		return err
	}
	if branch != tx.branch || head != tx.head || original != tx.original {
		return ErrIndexStateChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !tx.ownsLock() {
		return ErrIndexBusy
	}
	return nil
}

func (tx *indexTransaction) ownsLock() bool {
	info, err := tx.root.Lstat("index.lock")
	return err == nil && tx.lockInfo != nil && os.SameFile(info, tx.lockInfo)
}

func (tx *indexTransaction) Close() error {
	if tx.root == nil {
		return nil
	}
	var err error
	if tx.lock != nil {
		err = tx.lock.Close()
		tx.lock = nil
	}
	for _, name := range []string{tx.scratch, tx.scratch + ".lock"} {
		if removeErr := tx.root.Remove(name); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}
	if !tx.published && tx.ownsLock() {
		err = errors.Join(err, tx.root.Remove("index.lock"))
	}
	err = errors.Join(err, tx.root.Close())
	tx.root = nil
	return err
}

func copyIndex(ctx context.Context, root *os.Root, name string, output io.Writer) (string, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return "", ErrInvalidIndexStatus
	}
	input, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, opened) {
		return "", ErrIndexStateChanged
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(output, hash), io.LimitReader(reviewContextReader{ctx, input}, (64<<20)+1))
	if err != nil {
		return "", err
	}
	if size != info.Size() {
		return "", ErrIndexStateChanged
	}
	return hex.EncodeToString(hash.Sum(nil)), ctx.Err()
}
