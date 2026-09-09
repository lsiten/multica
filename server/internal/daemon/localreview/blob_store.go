package localreview

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sync"
)

var ErrSnapshotContentChanged = errors.New("cached review content changed; reload the review")
var ErrSnapshotBlobTooLarge = errors.New("file exceeds the local snapshot cache limit")
var ErrInvalidSnapshotBlob = errors.New("invalid snapshot content reference")

type BlobRef struct {
	ID   string `json:"id"`
	Size int64  `json:"size"`
}

type BlobStore struct {
	root                                    *os.Root
	catalog                                 *os.Root
	maxBytes                                int64
	budgetMu                                sync.Mutex
	budgetBytes, metadataReserve, usedBytes int64
	usageKnown                              bool
}

// OpenBlobStore keeps review data outside the checkout in the authorized task root.
// All subsequent filesystem operations stay rooted, even if paths are renamed.
func OpenBlobStore(taskRoot string, maxBytes int64) (*BlobStore, error) {
	if maxBytes < 1 || maxBytes > 1<<40 {
		return nil, ErrInvalidSnapshotBlob
	}
	task, err := os.OpenRoot(taskRoot)
	if err != nil {
		return nil, err
	}
	defer task.Close()
	cache, err := openCacheDirectory(task, ".local-review-cache")
	if err != nil {
		return nil, err
	}
	defer cache.Close()
	blobs, err := openCacheDirectory(cache, "blobs")
	if err != nil {
		return nil, err
	}
	catalog, err := openCacheDirectory(cache, "versions")
	if err != nil {
		blobs.Close()
		return nil, err
	}
	return &BlobStore{root: blobs, catalog: catalog, maxBytes: maxBytes, budgetBytes: 1 << 30, metadataReserve: 32 << 20}, nil
}

func openCacheDirectory(parent *os.Root, name string) (*os.Root, error) {
	if err := parent.Mkdir(name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidSnapshotBlob
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := child.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		child.Close()
		return nil, ErrInvalidSnapshotBlob
	}
	return child, nil
}

func (s *BlobStore) Close() error { return errors.Join(s.root.Close(), s.catalog.Close()) }

// Put captures bytes under their content digest without buffering the whole file.
// A failed/oversized capture never publishes a partial content reference.
func (s *BlobStore) Put(ctx context.Context, input io.Reader) (BlobRef, error) {
	return s.put(ctx, input, false)
}

func (s *BlobStore) put(ctx context.Context, input io.Reader, metadata bool) (BlobRef, error) {
	name := ".capture-" + rand.Text()
	file, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return BlobRef{}, err
	}
	defer s.root.Remove(name)
	hash := sha256.New()
	size, copyErr := io.CopyBuffer(io.MultiWriter(file, hash), io.LimitReader(reviewContextReader{ctx, input}, s.maxBytes+1), make([]byte, 64<<10))
	if copyErr == nil && size > s.maxBytes {
		copyErr = ErrSnapshotBlobTooLarge
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return BlobRef{}, err
	}
	if err := ctx.Err(); err != nil {
		return BlobRef{}, err
	}
	blob := BlobRef{ID: hex.EncodeToString(hash.Sum(nil)), Size: size}
	if err := s.publishCapture(ctx, name, blob, metadata); err != nil {
		return BlobRef{}, err
	}
	return blob, nil
}

type CachedPatchPage struct {
	Blob BlobRef
	Page PatchPageRequest
}

// PatchPage verifies the same stream used to produce the page before exposing it.
// Tampering cannot return partial data or silently mix content from another version.
func (s *BlobStore) PatchPage(ctx context.Context, request CachedPatchPage) (PatchPage, error) {
	page := PatchPage{}
	err := s.consume(ctx, request.Blob, func(reader io.Reader) error {
		var err error
		page, err = ReadPatchPage(ctx, reader, request.Page)
		return err
	})
	if err != nil {
		return PatchPage{}, err
	}
	return page, nil
}

func (s *BlobStore) consume(ctx context.Context, blob BlobRef, consume func(io.Reader) error) error {
	if !validBlobID(blob.ID) || blob.Size < 0 || blob.Size > s.maxBytes {
		return ErrInvalidSnapshotBlob
	}
	info, err := s.root.Lstat(blob.ID)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != blob.Size {
		return ErrSnapshotContentChanged
	}
	file, err := s.root.Open(blob.ID)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return ErrSnapshotContentChanged
	}
	hash := sha256.New()
	reader := io.TeeReader(io.LimitReader(reviewContextReader{ctx, file}, blob.Size+1), hash)
	if err := consume(reader); err != nil {
		return err
	}
	if _, err := io.CopyBuffer(io.Discard, reader, make([]byte, 64<<10)); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != blob.ID {
		return ErrSnapshotContentChanged
	}
	return ctx.Err()
}

func validBlobID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

type reviewContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r reviewContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
