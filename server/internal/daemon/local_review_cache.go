package daemon

import (
	"context"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var reviewCacheSweeps = struct {
	sync.Mutex
	last map[string]time.Time
}{last: map[string]time.Time{}}

// maintainReviewCache is called under localReviewOperations; it never blocks a
// read because maintenance could not validate a corrupt or incomplete cache.
func (d *Daemon) maintainReviewCache(ctx context.Context, store *localreview.BlobStore, root string, force bool) {
	now := time.Now()
	reviewCacheSweeps.Lock()
	last := reviewCacheSweeps.last[root]
	if !force && !last.IsZero() && !now.Before(last) && now.Sub(last) < 5*time.Minute {
		reviewCacheSweeps.Unlock()
		return
	}
	if len(reviewCacheSweeps.last) >= 256 {
		oldest := ""
		var stamp time.Time
		for path, at := range reviewCacheSweeps.last {
			if oldest == "" || at.Before(stamp) {
				oldest, stamp = path, at
			}
		}
		delete(reviewCacheSweeps.last, oldest)
	}
	reviewCacheSweeps.last[root] = now
	reviewCacheSweeps.Unlock()
	maintenance, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := store.Maintain(maintenance, root, now); err != nil {
		d.logger.Warn("review cache maintenance deferred", "error", err)
	}
}

func (d *Daemon) maintainIdleReviewCache(ctx context.Context, root string) {
	if _, err := d.gcTaskDirOwner(root); err != nil {
		return
	}
	if _, err := os.Lstat(filepath.Join(root, ".local-review-cache")); err != nil {
		return
	}
	if !localReviewOperations.TryLock() {
		return
	}
	defer localReviewOperations.Unlock()
	store, err := localreview.OpenBlobStore(root, 64<<20)
	if err != nil {
		d.logger.Warn("review cache unavailable during maintenance", "error", err)
		return
	}
	defer store.Close()
	d.maintainReviewCache(ctx, store, root, false)
}
