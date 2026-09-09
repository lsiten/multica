package localreview

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"time"
)

// BeginRead protects initial capture before a version ID exists.
func (s *BlobStore) BeginRead(ctx context.Context, now time.Time) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := ".reader-" + rand.Text()
	file, err := s.catalog.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		s.catalog.Remove(name)
		return nil, err
	}
	if err := s.catalog.Chtimes(name, now, now); err != nil {
		s.catalog.Remove(name)
		return nil, err
	}
	return func() { s.catalog.Remove(name) }, nil
}

// HasActiveReview returns false for task roots that have never created a review cache.
func HasActiveReview(ctx context.Context, taskRoot string, now time.Time) (bool, error) {
	root, err := os.OpenRoot(taskRoot)
	if err != nil {
		return false, err
	}
	defer root.Close()
	info, err := root.Lstat(".local-review-cache")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, ErrInvalidReviewVersion
	}
	cache, err := root.OpenRoot(".local-review-cache")
	if err != nil {
		return false, err
	}
	defer cache.Close()
	catalog, err := cache.OpenRoot("versions")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer catalog.Close()
	entries, err := cacheEntries(ctx, catalog)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !validBlobID(entry.name) && !strings.HasPrefix(entry.name, ".reader-") {
			continue
		}
		if !entry.regular {
			return false, ErrInvalidReviewVersion
		}
		if now.Sub(entry.modified) <= ReviewLeaseTTL {
			return true, nil
		}
	}
	return false, nil
}
