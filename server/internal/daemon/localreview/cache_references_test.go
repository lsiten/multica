package localreview

import (
	"strings"
	"testing"
	"time"
)

func TestReferencedVersionsIncludesHistoryAndPreparedMerge(t *testing.T) {
	root := t.TempDir()
	a, b, c := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	record := Record{VersionID: a, SnapshotID: a, State: "approved", Events: []Event{{VersionID: b}}, PreparedRequest: &Event{VersionID: c}}
	if err := SaveRecord(root, strings.Repeat("d", 64), record); err != nil {
		t.Fatal(err)
	}
	ids, err := ReferencedVersions(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != strings.Join([]string{a, b, c}, ",") {
		t.Fatalf("protection references lost: %v", ids)
	}
}

func TestInitialReadLeaseProtectsCaptureBeforeManifestExists(t *testing.T) {
	root := t.TempDir()
	store, err := OpenBlobStore(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	release, err := store.BeginRead(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	active, err := HasActiveReview(t.Context(), root, now)
	if err != nil || !active {
		t.Fatal("capture was not protected", err)
	}
	release()
	active, err = HasActiveReview(t.Context(), root, now)
	if err != nil || active {
		t.Fatal("completed capture retained its in-flight lease", err)
	}
}
