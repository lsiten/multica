package localreview

import (
	"fmt"
	"strings"
	"testing"
)

func versionFixture() ReviewVersion {
	return ReviewVersion{Header: VersionHeader{Repository: "/repo", Branch: "feature", Target: "main", Head: strings.Repeat("1", 40), TargetHead: strings.Repeat("2", 40), Base: strings.Repeat("3", 40)}, Files: []VersionFile{}}
}

func TestVersionIdentityTracksContentAndTargetButNotFileOrder(t *testing.T) {
	store, err := OpenBlobStore(t.TempDir(), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a, err := store.Put(t.Context(), strings.NewReader("first"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Put(t.Context(), strings.NewReader("second"))
	if err != nil {
		t.Fatal(err)
	}
	version := versionFixture()
	version.Files = []VersionFile{{Path: "b.txt", Status: "added", New: &b, Preview: "text"}, {Path: "a.txt", Status: "added", New: &a, Preview: "text"}}
	first, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	version.Files[0], version.Files[1] = version.Files[1], version.Files[0]
	reordered, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	if reordered != first {
		t.Fatal("file enumeration changed version identity")
	}
	version.Files[0].New = &b
	changed, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("changed content retained approval identity")
	}
	version.Header.TargetHead = strings.Repeat("4", 40)
	advanced, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	if advanced == changed {
		t.Fatal("advanced target retained approval identity")
	}
}

func TestVersionFilePagesExcludePatchPayloads(t *testing.T) {
	store, err := OpenBlobStore(t.TempDir(), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version := versionFixture()
	blob, err := store.Put(t.Context(), strings.NewReader("captured content"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 205; i++ {
		version.Files = append(version.Files, VersionFile{Path: fmt.Sprintf("%04d.txt", i), Status: "modified", Old: &blob, New: &blob, Preview: "text", Additions: 2, Deletions: 1})
	}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadVersion(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	page, err := loaded.FilePage(FilePageRequest{Offset: 100, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalFiles != 205 || len(page.Files) != 100 || page.NextOffset != 200 || !page.HasMore || page.Additions != 410 || page.Deletions != 205 || page.Files[0].Path != "0100.txt" {
		t.Fatalf("bad page: %+v", page)
	}
	last, err := loaded.FilePage(FilePageRequest{Offset: page.NextOffset, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Files) != 5 || last.HasMore {
		t.Fatalf("bad last page: %+v", last)
	}
}

func TestVersionRejectsDuplicateOrEscapingFileNames(t *testing.T) {
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, files := range [][]VersionFile{
		{{Path: "../outside", Status: "added", Preview: "text"}},
		{{Path: "same", Status: "added", Preview: "text"}, {Path: "same", Status: "modified", Preview: "text"}},
	} {
		version := versionFixture()
		version.Files = files
		if _, err := store.SaveVersion(t.Context(), version); err == nil {
			t.Fatal("accepted invalid manifest")
		}
	}
}
