package localreview

import (
	"strings"
	"testing"
)

func TestStagingUsesVerifiedUncachedWorkingContent(t *testing.T) {
	repo := repository(t)
	contents := strings.Repeat("uncached large contents\n", 4000)
	write(t, repo, "app.txt", contents)
	// The content exceeds this store's per-blob limit; metadata still fits.
	store, err := OpenBlobStore(t.TempDir(), 8<<10)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if version.Files[0].NewCached {
		t.Fatal("fixture unexpectedly cached the content")
	}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	status, err := ReadIndexStatus(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ChangeStaging(t.Context(), IndexSelection{Version: VersionSelection{ID: id, Path: version.Header.Repository, Target: "feature"}, IndexID: status.IndexID, Paths: []string{"app.txt"}}); err != nil {
		t.Fatal(err)
	}
	if got := run(t, repo, "show", ":app.txt"); got != strings.TrimSpace(contents) {
		t.Fatal("staged content mismatch")
	}
}
