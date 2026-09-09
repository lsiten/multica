package localreview

import (
	"context"
	"slices"
	"strings"
	"time"
)

type CachePruneRequest struct {
	Now              time.Time
	MaxBytes         int64
	LeaseTTL, MaxAge time.Duration
	Protected        []string
}
type CachePruneResult struct {
	RemovedVersions, RemovedBlobs         int
	BudgetUsedBytes, BudgetReclaimedBytes int64
	QuotaSatisfied                        bool
}
type cacheVersion struct {
	id        string
	access    time.Time
	protected bool
}

func versionReferences(id string, version ReviewVersion) ([]string, error) {
	refs := map[string]bool{id: true}
	for _, file := range version.Files {
		for _, side := range []struct {
			blob   *BlobRef
			cached bool
		}{{file.Old, file.OldCached}, {file.New, file.NewCached}} {
			if !side.cached {
				continue
			}
			if side.blob == nil {
				return nil, ErrInvalidReviewVersion
			}
			refs[side.blob.ID] = true
		}
	}
	result := make([]string, 0, len(refs))
	for id := range refs {
		result = append(result, id)
	}
	return result, nil
}

// Prune is a two-phase mark/sweep. Callers serialize operations for a task cache.
// Unknown entries are never removed; referenced and leased content stays intact.
func (s *BlobStore) Prune(ctx context.Context, request CachePruneRequest) (CachePruneResult, error) {
	result := CachePruneResult{}
	if request.Now.IsZero() || request.MaxBytes < 4096 || request.LeaseTTL <= 0 || request.MaxAge < request.LeaseTTL {
		return result, ErrInvalidReviewVersion
	}
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	blobs, err := cacheEntries(ctx, s.root)
	if err != nil {
		return result, err
	}
	markers, err := cacheEntries(ctx, s.catalog)
	if err != nil {
		return result, err
	}
	used, err := s.measureUsage(ctx)
	if err != nil {
		return result, err
	}
	result.BudgetUsedBytes = used
	byID := map[string]cacheEntry{}
	for _, entry := range blobs {
		byID[entry.name] = entry
	}
	protected := map[string]bool{}
	for _, id := range request.Protected {
		protected[id] = true
	}
	nodes := []cacheVersion{}
	references := map[string]int64{}
	for _, entry := range markers {
		if !validBlobID(entry.name) {
			continue
		}
		if !entry.regular {
			return result, ErrInvalidReviewVersion
		}
		version, err := s.LoadVersion(ctx, entry.name)
		if err != nil {
			return result, err
		}
		refs, err := versionReferences(entry.name, version)
		if err != nil {
			return result, err
		}
		for _, id := range refs {
			if blob, ok := byID[id]; !ok || !blob.regular {
				return result, ErrSnapshotContentChanged
			}
			references[id]++
		}
		nodes = append(nodes, cacheVersion{entry.name, entry.modified, protected[entry.name]})
		delete(protected, entry.name)
	}
	if len(protected) > 0 {
		return result, ErrSnapshotContentChanged
	}
	slices.SortFunc(nodes, func(a, b cacheVersion) int { return a.access.Compare(b.access) })
	slices.SortFunc(blobs, func(a, b cacheEntry) int { return a.modified.Compare(b.modified) })
	dropBlobs := map[string]bool{}
	dropVersions := []string{}
	planBlob := func(entry cacheEntry) {
		if !entry.regular || !validBlobID(entry.name) || references[entry.name] > 0 || request.Now.Sub(entry.modified) <= request.LeaseTTL || dropBlobs[entry.name] {
			return
		}
		dropBlobs[entry.name] = true
		used -= cacheCharge(entry.size)
	}
	for _, entry := range blobs {
		if request.Now.Sub(entry.modified) > request.MaxAge || used > request.MaxBytes {
			planBlob(entry)
		}
	}
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if node.protected || request.Now.Sub(node.access) <= request.LeaseTTL {
			continue
		}
		if request.Now.Sub(node.access) <= request.MaxAge && used <= request.MaxBytes {
			continue
		}
		version, err := s.LoadVersion(ctx, node.id)
		if err != nil {
			return result, err
		}
		refs, err := versionReferences(node.id, version)
		if err != nil {
			return result, err
		}
		dropVersions = append(dropVersions, node.id)
		used -= 4096
		for _, id := range refs {
			references[id]--
			planBlob(byID[id])
		}
	}
	// No deletion occurs until all versions and their references were validated.
	for _, id := range dropVersions {
		if err := ctx.Err(); err != nil {
			s.usageKnown = false
			return result, err
		}
		if err := s.catalog.Remove(id); err != nil {
			s.usageKnown = false
			return result, err
		}
		result.RemovedVersions++
		result.BudgetUsedBytes -= 4096
		result.BudgetReclaimedBytes += 4096
	}
	for _, entry := range markers {
		if entry.regular && strings.HasPrefix(entry.name, ".reader-") && request.Now.Sub(entry.modified) > request.LeaseTTL {
			if err := s.catalog.Remove(entry.name); err != nil {
				s.usageKnown = false
				return result, err
			}
		}
	}
	for _, entry := range blobs {
		orphanCapture := entry.regular && strings.HasPrefix(entry.name, ".capture-") && request.Now.Sub(entry.modified) > request.MaxAge
		if !dropBlobs[entry.name] && !orphanCapture {
			continue
		}
		if err := ctx.Err(); err != nil {
			s.usageKnown = false
			return result, err
		}
		if err := s.root.Remove(entry.name); err != nil {
			s.usageKnown = false
			return result, err
		}
		result.RemovedBlobs++
		if !orphanCapture {
			cost := cacheCharge(entry.size)
			result.BudgetUsedBytes -= cost
			result.BudgetReclaimedBytes += cost
		}
	}
	result.QuotaSatisfied = result.BudgetUsedBytes <= request.MaxBytes
	s.usedBytes, s.usageKnown = result.BudgetUsedBytes, true
	return result, nil
}
