package localreview

import (
	"strings"
	"testing"
)

func TestMergeCapturedVersionLargerThanOldPatchLimit(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", strings.Repeat("large reviewed line\n", 500000))
	run(t, repo, "commit", "-am", "large feature")
	source := run(t, repo, "rev-parse", "HEAD")
	store, err := OpenBlobStore(t.TempDir(), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	prepared := ""
	commit, err := store.MergeVersionPrepared(t.Context(), VersionSelection{ID: id, Path: version.Header.Repository, Target: "main"}, func(commit string) error { prepared = commit; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if commit == "" || prepared != commit || run(t, repo, "rev-parse", "main") != commit || run(t, repo, "rev-parse", "main^2") != source {
		t.Fatal("reviewed source was not merged")
	}
}

func TestMergeCapturedVersionRejectsChangedSource(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "reviewed\n")
	run(t, repo, "commit", "-am", "first")
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	target := run(t, repo, "rev-parse", "main")
	write(t, repo, "app.txt", "not reviewed\n")
	run(t, repo, "commit", "-am", "second")
	if _, err := store.MergeVersionPrepared(t.Context(), VersionSelection{ID: id, Path: version.Header.Repository, Target: "main"}, nil); err == nil {
		t.Fatal("stale review merged changed source")
	}
	if run(t, repo, "rev-parse", "main") != target {
		t.Fatal("rejected merge changed target")
	}
}

func TestMergeCapturedVersionPreservesTargetCAS(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "reviewed\n")
	run(t, repo, "commit", "-am", "first")
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.MergeVersionPrepared(t.Context(), VersionSelection{ID: id, Path: version.Header.Repository, Target: "main"}, func(string) error {
		run(t, repo, "update-ref", "refs/heads/main", version.Header.Head)
		return nil
	})
	if err == nil {
		t.Fatal("merge overwrote concurrently moved target")
	}
	if run(t, repo, "rev-parse", "main") != version.Header.Head {
		t.Fatal("target CAS was bypassed")
	}
}
