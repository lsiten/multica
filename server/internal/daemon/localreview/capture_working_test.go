package localreview

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureWorkingFreezesStagedUnstagedAndUntrackedContent(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "staged\n")
	run(t, repo, "add", "app.txt")
	write(t, repo, "app.txt", "actual working contents\n")
	write(t, repo, "untracked.txt", strings.Repeat("large local line\n", 600000))
	before := run(t, repo, "status", "--porcelain")
	store, err := OpenBlobStore(t.TempDir(), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if !version.Header.Dirty || version.Header.Committed || len(version.Files) != 2 {
		t.Fatalf("bad version: %+v", version)
	}
	if version.Files[1].Status != "untracked" || version.Files[1].Additions != 600000 || version.Files[1].New.Size <= 8<<20 {
		t.Fatalf("untracked file missing: %+v", version.Files[1])
	}
	patch, err := store.FilePatch(t.Context(), version.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.PatchPage(t.Context(), CachedPatchPage{Blob: patch, Page: PatchPageRequest{Limit: 30}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range page.Lines {
		if line.Text == "+actual working contents" {
			found = true
		}
	}
	if !found {
		t.Fatalf("wrong working version: %+v", page)
	}
	if before != run(t, repo, "status", "--porcelain") {
		t.Fatal("capture changed index or checkout")
	}
}

func TestCaptureWorkingOversizeFingerprintCoversEntireFile(t *testing.T) {
	repo := repository(t)
	content := strings.Repeat("a", 1000) + "last"
	write(t, repo, "large.txt", content)
	store, err := OpenBlobStore(t.TempDir(), 64)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(content))
	if len(version.Files) != 1 || version.Files[0].Preview != "too_large" || version.Files[0].New.ID != hex.EncodeToString(digest[:]) {
		t.Fatalf("incomplete large-file fingerprint: %+v", version.Files)
	}
	write(t, repo, "large.txt", strings.Repeat("a", 1000)+"next")
	changed, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Files[0].New.ID == version.Files[0].New.ID {
		t.Fatal("change beyond cache limit was missed")
	}
}

func TestCaptureWorkingSymlinkStoresLinkTextNotExternalContent(t *testing.T) {
	repo := repository(t)
	external := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(external, []byte("private contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(repo, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(version.Files) != 1 || version.Files[0].NewMode != "120000" || version.Files[0].New.Size != int64(len(external)) {
		t.Fatalf("symlink was followed: %+v", version.Files)
	}
}

func TestCaptureWorkingChangesVersionWithoutGitStatusTransition(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "first dirty contents\n")
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := store.SaveVersion(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	status := run(t, repo, "status", "--porcelain")
	write(t, repo, "app.txt", "second dirty contents\n")
	if status != run(t, repo, "status", "--porcelain") {
		t.Fatal("fixture must retain the same status")
	}
	second, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := store.SaveVersion(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	if firstID == secondID {
		t.Fatal("same-status content edit did not invalidate version")
	}
	loaded, err := store.LoadVersion(t.Context(), firstID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Files[0].New.ID != first.Files[0].New.ID {
		t.Fatal("old version was rewritten")
	}
}
