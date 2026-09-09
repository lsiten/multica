package execenv

import (
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveReviewDirectoryRetainsVersionHistoryContent(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "ws", "task")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeEnvRootOwner(root, "ws", "task"); err != nil {
		t.Fatal(err)
	}
	store, err := localreview.OpenBlobStore(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	header := localreview.VersionHeader{Repository: filepath.Join(workspace, "repo"), Branch: "feature", Target: "main", Head: strings.Repeat("1", 40), TargetHead: strings.Repeat("2", 40), Base: strings.Repeat("3", 40)}
	ids := []string{}
	for _, text := range []string{"earlier reviewed content", "current reviewed content"} {
		blob, err := store.Put(t.Context(), strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		id, err := store.SaveVersion(t.Context(), localreview.ReviewVersion{Header: header, Files: []localreview.VersionFile{{Path: "file.txt", Status: "added", New: &blob, NewCached: true, Preview: "text"}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	key := localreview.RecordKey(localreview.Snapshot{Path: header.Repository, Target: header.Target})
	record := localreview.Record{SnapshotID: ids[1], VersionID: ids[1], State: "approved", Events: []localreview.Event{{Kind: "approve", SnapshotID: ids[0], VersionID: ids[0]}}}
	if err := localreview.SaveRecord(root, key, record); err != nil {
		t.Fatal(err)
	}
	if err := ArchiveReviewDirectory(t.Context(), workspace, root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	archived, err := localreview.OpenBlobStore(ReviewArchivePath(workspace, "ws", "task"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer archived.Close()
	for i, id := range ids {
		version, err := archived.LoadVersion(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		content, err := archived.ContentPage(t.Context(), localreview.CachedContentPage{Blob: *version.Files[0].New, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"earlier reviewed content", "current reviewed content"}[i]
		if content.Text != want {
			t.Fatalf("version %d content=%q", i, content.Text)
		}
	}
}
