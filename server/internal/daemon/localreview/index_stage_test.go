package localreview

import "testing"

func TestStageAndUnstagePreserveOtherFilesAndWorkingBytes(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "selected contents\n")
	write(t, repo, "other.txt", "other contents\n")
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	status, err := ReadIndexStatus(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	selection := IndexSelection{Version: VersionSelection{ID: id, Path: version.Header.Repository, Target: "feature"}, IndexID: status.IndexID, Paths: []string{"app.txt"}}
	if err := store.ChangeStaging(t.Context(), selection); err != nil {
		t.Fatal(err)
	}
	if got := run(t, repo, "diff", "--cached", "--name-only"); got != "app.txt" {
		t.Fatal("staged unrelated changes", got)
	}
	if got := run(t, repo, "show", ":app.txt"); got != "selected contents" {
		t.Fatal("wrong staged content", got)
	}
	status, err = ReadIndexStatus(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	selection.IndexID, selection.Unstage = status.IndexID, true
	if err := store.ChangeStaging(t.Context(), selection); err != nil {
		t.Fatal(err)
	}
	if got := run(t, repo, "diff", "--cached", "--name-only"); got != "" {
		t.Fatal("unstage did not restore HEAD", got)
	}
	if got := run(t, repo, "diff", "--name-only"); got != "app.txt" {
		t.Fatal("working edit was lost", got)
	}
}

func TestStagingRejectsStaleWorkingContentOrIndex(t *testing.T) {
	for _, changeIndex := range []bool{false, true} {
		t.Run(map[bool]string{false: "working", true: "index"}[changeIndex], func(t *testing.T) {
			repo := repository(t)
			write(t, repo, "app.txt", "reviewed\n")
			write(t, repo, "other.txt", "unrelated\n")
			store, err := OpenBlobStore(t.TempDir(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "feature"})
			if err != nil {
				t.Fatal(err)
			}
			id, err := store.SaveVersion(t.Context(), version)
			if err != nil {
				t.Fatal(err)
			}
			status, err := ReadIndexStatus(t.Context(), repo)
			if err != nil {
				t.Fatal(err)
			}
			if changeIndex {
				run(t, repo, "add", "other.txt")
			} else {
				write(t, repo, "app.txt", "not reviewed\n")
			}
			before := run(t, repo, "diff", "--cached", "--name-only")
			err = store.ChangeStaging(t.Context(), IndexSelection{Version: VersionSelection{ID: id, Path: version.Header.Repository, Target: "feature"}, IndexID: status.IndexID, Paths: []string{"app.txt"}})
			if err == nil {
				t.Fatal("stale staging request succeeded")
			}
			if got := run(t, repo, "diff", "--cached", "--name-only"); got != before {
				t.Fatal("failed request changed staged paths", got)
			}
		})
	}
}
