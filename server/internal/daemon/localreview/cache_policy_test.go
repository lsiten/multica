package localreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCacheBudgetRejectsNewDataButAllowsDeduplication(t *testing.T) {
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.budgetBytes = 3 * 4096
	store.metadataReserve = 4096
	first, err := store.Put(t.Context(), strings.NewReader("first"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), strings.NewReader("second")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), strings.NewReader("third")); err != ErrSnapshotCacheFull {
		t.Fatalf("expected cache budget error, got %v", err)
	}
	same, err := store.Put(t.Context(), strings.NewReader("first"))
	if err != nil || same != first {
		t.Fatalf("deduplication used more quota: %+v %v", same, err)
	}
}

func TestCachePrunePreservesLeasedAndRecordedVersions(t *testing.T) {
	root := t.TempDir()
	store, err := OpenBlobStore(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ids := []string{}
	for _, text := range []string{"recorded", "active", "expired"} {
		body, err := store.Put(t.Context(), strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		version := versionFixture()
		version.Files = []VersionFile{{Path: "file.txt", Status: "added", New: &body, NewCached: true, Preview: "text"}}
		id, err := store.SaveVersion(t.Context(), version)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	scratch, err := store.Put(t.Context(), strings.NewReader("unused patch"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(2 * time.Hour)
	if err := store.TouchVersion(t.Context(), ids[1], now); err != nil {
		t.Fatal(err)
	}
	active, err := store.LoadVersion(t.Context(), ids[1])
	if err != nil {
		t.Fatal(err)
	}
	sameID, err := store.SaveVersion(t.Context(), active)
	if err != nil || sameID != ids[1] {
		t.Fatal("lease renewal changed version identity")
	}
	result, err := store.Prune(t.Context(), CachePruneRequest{Now: now, MaxBytes: 1 << 20, LeaseTTL: 5 * time.Minute, MaxAge: time.Hour, Protected: []string{ids[0]}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedVersions != 1 {
		t.Fatalf("unexpected pruning: %+v", result)
	}
	for _, id := range ids[:2] {
		if _, err := store.LoadVersion(t.Context(), id); err != nil {
			t.Fatal("protected version was removed", err)
		}
	}
	if _, err := store.LoadVersion(t.Context(), ids[2]); !os.IsNotExist(err) {
		t.Fatalf("expired version remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".local-review-cache", "blobs", scratch.ID)); !os.IsNotExist(err) {
		t.Fatal("expired scratch remains")
	}
	pressure, err := store.Prune(t.Context(), CachePruneRequest{Now: now, MaxBytes: 4096, LeaseTTL: 5 * time.Minute, MaxAge: time.Hour, Protected: []string{ids[0]}})
	if err != nil {
		t.Fatal(err)
	}
	if pressure.QuotaSatisfied || pressure.RemovedVersions != 0 || pressure.RemovedBlobs != 0 {
		t.Fatalf("budget pressure broke protection: %+v", pressure)
	}
}

func TestCacheReserveAllowsUncachedManifestAfterDataBudgetFills(t *testing.T) {
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.budgetBytes = 4 * 4096
	store.metadataReserve = 2 * 4096
	for _, value := range []string{"first", "second"} {
		if _, err := store.Put(t.Context(), strings.NewReader(value)); err != nil {
			t.Fatal(err)
		}
	}
	captured, err := store.CaptureContent(t.Context(), strings.NewReader("not cached but fingerprinted"))
	if err != nil {
		t.Fatal(err)
	}
	if captured.Cached || !captured.CacheLimited {
		t.Fatal("cache pressure was not explicit")
	}
	version := versionFixture()
	version.Files = []VersionFile{{Path: "file.txt", Status: "added", New: &captured.Blob, Preview: "uncached"}}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal("metadata reserve was unavailable", err)
	}
	if _, err := store.LoadVersion(t.Context(), id); err != nil {
		t.Fatal(err)
	}
}
