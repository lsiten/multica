package localreview

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"time"
)

const DefaultCacheBudget int64 = 1 << 30
const ReviewLeaseTTL = 5 * time.Minute
const ReviewCacheMaxAge = 24 * time.Hour

// ReferencedVersions reads runtime-owned receipts without following replaced links.
func ReferencedVersions(ctx context.Context, taskRoot string) ([]string, error) {
	root, err := os.OpenRoot(taskRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	entries, err := cacheEntries(ctx, root)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, entry := range entries {
		key := strings.TrimSuffix(strings.TrimPrefix(entry.name, ".local-review-"), ".json")
		if !strings.HasPrefix(entry.name, ".local-review-") || !strings.HasSuffix(entry.name, ".json") || !validBlobID(key) {
			continue
		}
		if !entry.regular || entry.size > 12<<20 {
			return nil, ErrInvalidReviewVersion
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := root.Lstat(entry.name)
		if err != nil {
			return nil, err
		}
		file, err := root.Open(entry.name)
		if err != nil {
			return nil, err
		}
		opened, statErr := file.Stat()
		if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			file.Close()
			return nil, ErrInvalidReviewVersion
		}
		var record Record
		decoder := json.NewDecoder(io.LimitReader(file, (12<<20)+1))
		decodeErr := decoder.Decode(&record)
		if decodeErr == nil {
			if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
				decodeErr = ErrInvalidReviewVersion
			}
		}
		closeErr := file.Close()
		if err := errors.Join(decodeErr, closeErr); err != nil {
			return nil, err
		}
		for _, id := range RecordVersionIDs(record) {
			if !validBlobID(id) {
				return nil, ErrInvalidReviewVersion
			}
			ids[id] = true
		}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	slices.Sort(result)
	return result, nil
}

func (s *BlobStore) Maintain(ctx context.Context, taskRoot string, now time.Time) (CachePruneResult, error) {
	ids, err := ReferencedVersions(ctx, taskRoot)
	if err != nil {
		return CachePruneResult{}, err
	}
	return s.Prune(ctx, CachePruneRequest{Now: now, MaxBytes: s.budgetBytes, LeaseTTL: ReviewLeaseTTL, MaxAge: ReviewCacheMaxAge, Protected: ids})
}
