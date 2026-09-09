package localreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveVersionsPreservesReferencedContentOnly(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	store, err := OpenBlobStore(source, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	body, err := store.Put(t.Context(), strings.NewReader("reviewed content"))
	if err != nil {
		t.Fatal(err)
	}
	unused, err := store.Put(t.Context(), strings.NewReader("unreferenced scratch"))
	if err != nil {
		t.Fatal(err)
	}
	version := versionFixture()
	version.Files = []VersionFile{{Path: "file.txt", Status: "added", New: &body, NewCached: true, Preview: "text"}}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ArchiveVersions(t.Context(), VersionArchive{Source: source, Destination: destination, IDs: []string{id}}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	archived, err := OpenBlobStore(destination, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer archived.Close()
	loaded, err := archived.LoadVersion(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	page, err := archived.ContentPage(t.Context(), CachedContentPage{Blob: *loaded.Files[0].New, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Text != "reviewed content" {
		t.Fatal("archived content changed")
	}
	if _, err := os.Stat(filepath.Join(destination, ".local-review-cache", "blobs", unused.ID)); !os.IsNotExist(err) {
		t.Fatal("unreferenced scratch was archived")
	}
}

func TestArchiveVersionsRejectsMissingRequiredCache(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	store, err := OpenBlobStore(source, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version := versionFixture()
	version.Files = []VersionFile{{Path: "file.txt", Status: "added", New: &BlobRef{ID: strings.Repeat("a", 64), Size: 10}, NewCached: true, Preview: "text"}}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	if err := ArchiveVersions(t.Context(), VersionArchive{Source: source, Destination: destination, IDs: []string{id}}); err == nil {
		t.Fatal("archive silently dropped required content")
	}
}
