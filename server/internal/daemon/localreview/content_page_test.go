package localreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContentPageSupportsLongLinesAndUTF8Boundaries(t *testing.T) {
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	text := "中" + strings.Repeat("文", 30000)
	blob, err := store.Put(t.Context(), strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ContentPage(t.Context(), CachedContentPage{Blob: blob, Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if first.Encoding != "utf8" || first.Text != "中文" || first.NextOffset != 6 || !first.HasMore {
		t.Fatalf("UTF8 was split: %+v", first)
	}
	next, err := store.ContentPage(t.Context(), CachedContentPage{Blob: blob, Offset: first.NextOffset, Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if next.Text != "文文" || next.NextOffset != 12 {
		t.Fatalf("continuation lost bytes: %+v", next)
	}
}

func TestContentPageDisplaysBinaryAsHex(t *testing.T) {
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	blob, err := store.Put(t.Context(), strings.NewReader("\x00\xff\x01\x02"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.ContentPage(t.Context(), CachedContentPage{Blob: blob, Limit: 2, Binary: true})
	if err != nil {
		t.Fatal(err)
	}
	if page.Encoding != "hex" || page.Text != "00 ff" || page.NextOffset != 2 || !page.HasMore {
		t.Fatalf("bad binary page: %+v", page)
	}
}

func TestContentPageDoesNotExposeTamperedBytes(t *testing.T) {
	root := t.TempDir()
	store, err := OpenBlobStore(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	blob, err := store.Put(t.Context(), strings.NewReader("original"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".local-review-cache", "blobs", blob.ID), []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := store.ContentPage(t.Context(), CachedContentPage{Blob: blob, Limit: 4})
	if err == nil || page.Text != "" {
		t.Fatal("tampered bytes were exposed")
	}
}
