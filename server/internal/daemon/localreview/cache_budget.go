package localreview

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

var ErrSnapshotCacheFull = errors.New("local review cache capacity reached; existing reviews were preserved")

type cacheEntry struct {
	name     string
	size     int64
	modified time.Time
	regular  bool
}

// Charging a minimum allocation unit also bounds caches with many tiny objects.
func cacheCharge(size int64) int64 { return max(int64(4096), ((size+4095)/4096)*4096) }

func cacheEntries(ctx context.Context, root *os.Root) ([]cacheEntry, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries := []cacheEntry{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := directory.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		for _, entry := range batch {
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			if info.Size() < 0 || info.Size() > 1<<40 || len(entries) >= 1<<20 {
				return nil, ErrSnapshotCacheFull
			}
			entries = append(entries, cacheEntry{entry.Name(), info.Size(), info.ModTime(), info.Mode().IsRegular()})
		}
		if errors.Is(err, io.EOF) {
			return entries, nil
		}
	}
}

func (s *BlobStore) measureUsage(ctx context.Context) (int64, error) {
	var used int64
	for _, root := range []*os.Root{s.root, s.catalog} {
		entries, err := cacheEntries(ctx, root)
		if err != nil {
			return 0, err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.name, ".capture-") || strings.HasPrefix(entry.name, ".reader-") {
				continue
			}
			used += cacheCharge(entry.size)
		}
	}
	return used, nil
}

func (s *BlobStore) publishCapture(ctx context.Context, temporary string, blob BlobRef, metadata bool) error {
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	if !s.usageKnown {
		used, err := s.measureUsage(ctx)
		if err != nil {
			return err
		}
		s.usedBytes, s.usageKnown = used, true
	}
	delta := cacheCharge(blob.Size)
	if existing, err := s.root.Lstat(blob.ID); err == nil {
		if !existing.Mode().IsRegular() {
			return ErrInvalidSnapshotBlob
		}
		delta -= cacheCharge(existing.Size())
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	markerCost := int64(0)
	if metadata {
		if info, err := s.catalog.Lstat(blob.ID); err == nil {
			if !info.Mode().IsRegular() {
				return ErrInvalidSnapshotBlob
			}
		} else if errors.Is(err, os.ErrNotExist) {
			markerCost = 4096
		} else {
			return err
		}
	}
	limit := s.budgetBytes
	if !metadata {
		limit -= s.metadataReserve
	}
	if delta+markerCost > 0 && delta+markerCost > limit-s.usedBytes {
		return ErrSnapshotCacheFull
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.root.Rename(temporary, blob.ID); err != nil {
		return err
	}
	s.usedBytes += delta
	if markerCost > 0 {
		file, err := s.catalog.OpenFile(blob.ID, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			s.usageKnown = false
			return err
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(syncErr, closeErr); err != nil {
			s.usageKnown = false
			return err
		}
		s.usedBytes += markerCost
	}
	return nil
}

// TouchVersion renews a previously registered version's read lease.
func (s *BlobStore) TouchVersion(ctx context.Context, id string, now time.Time) error {
	if !validBlobID(id) || now.IsZero() {
		return ErrInvalidReviewVersion
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	info, err := s.catalog.Lstat(id)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrInvalidReviewVersion
	}
	return s.catalog.Chtimes(id, now, now)
}
