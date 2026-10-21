package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupInventoryGroupsInternalFilesAndSkipsSymlinks(t *testing.T) {
	// Given
	dir := t.TempDir()
	store := &LocalStorage{uploadDir: dir}
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, name := range []string{"blob", "blob.meta.json", ".blob.tmp", "orphan.meta.json", ".partial.tmp"} {
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}

	// When
	objects, err := store.CleanupInventory(context.Background(), old.Add(time.Hour))

	// Then
	if err := err; err != nil {
		t.Fatal(err)
	}
	if len(objects) != 3 {
		t.Fatalf("unexpected length: %d", len(objects))
	}
	byKey := make(map[string]LocalUploadObject)
	for _, object := range objects {
		byKey[object.Key] = object
	}
	if got, want := byKey["blob"].Bytes, int64(12); got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
	if byKey["blob"].Recent {
		t.Fatal("expected false")
	}
	if _, ok := byKey["orphan"]; !ok {
		t.Fatal("missing object key")
	}
	if _, ok := byKey["partial"]; !ok {
		t.Fatal("missing object key")
	}
}

func TestCleanupInventoryProtectsWholeGroupWhenOneFileIsRecent(t *testing.T) {
	// Given
	dir := t.TempDir()
	store := &LocalStorage{uploadDir: dir}
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(dir, "blob"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "blob"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".blob.tmp"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}

	// When
	objects, err := store.CleanupInventory(context.Background(), old.Add(time.Hour))

	// Then
	if err := err; err != nil {
		t.Fatal(err)
	}
	if len(objects) != 1 {
		t.Fatalf("unexpected length: %d", len(objects))
	}
	if !objects[0].Recent {
		t.Fatal("expected true")
	}
}

func TestDeleteCleanupObjectRemovesObjectSidecarAndStagingFile(t *testing.T) {
	// Given
	dir := t.TempDir()
	store := &LocalStorage{uploadDir: dir}
	for _, name := range []string{"blob", "blob.meta.json", ".blob.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	objects, err := store.CleanupInventory(context.Background(), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	if err := err; err != nil {
		t.Fatal(err)
	}

	// When
	err = store.DeleteCleanupObject(context.Background(), objects[0])

	// Then
	if err := err; err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err := err; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty collection: %v", entries)
	}
}

func TestDeleteCleanupObjectKeepsFilesChangedAfterScan(t *testing.T) {
	// Given
	dir := t.TempDir()
	store := &LocalStorage{uploadDir: dir}
	file := filepath.Join(dir, "blob")
	if err := os.WriteFile(file, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	objects, err := store.CleanupInventory(context.Background(), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	if err := err; err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("new upload"), 0600); err != nil {
		t.Fatal(err)
	}

	// When
	err = store.DeleteCleanupObject(context.Background(), objects[0])

	// Then
	if err == nil {
		t.Fatal("expected an error")
	}
	body, err := os.ReadFile(file)
	if err := err; err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "new upload"; got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDeleteCleanupObjectRefusesReplacedDirectorySymlink(t *testing.T) {
	// Given
	dir, outside := t.TempDir(), t.TempDir()
	store := &LocalStorage{uploadDir: dir}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "blob"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	objects, err := store.CleanupInventory(context.Background(), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	if err := err; err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "blob"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}

	// When
	err = store.DeleteCleanupObject(context.Background(), objects[0])

	// Then
	if err == nil {
		t.Fatal("expected an error")
	}
	body, err := os.ReadFile(filepath.Join(outside, "blob"))
	if err := err; err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "outside"; got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}
