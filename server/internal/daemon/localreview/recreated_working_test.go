package localreview

import "testing"

func TestRecreatedWorkingFileCanBeReviewedAndStaged(t *testing.T) {
	repo := repository(t)
	run(t, repo, "rm", "--cached", "app.txt")
	write(t, repo, "app.txt", "recreated content\n")
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if len(version.Files) != 1 || version.Files[0].Status != "modified" || version.Files[0].Additions != 1 || version.Files[0].Deletions != 1 {
		t.Fatalf("wrong net comparison: %+v", version.Files)
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
	if got := run(t, repo, "show", ":app.txt"); got != "recreated content" {
		t.Fatal("recreated bytes not staged", got)
	}
}

func TestRecreatedWorkingFileEqualToHEADHasNoNetDiff(t *testing.T) {
	repo := repository(t)
	run(t, repo, "rm", "--cached", "app.txt")
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if len(version.Files) != 0 {
		t.Fatalf("invented a net diff for unchanged bytes: %+v", version.Files)
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
	if got := run(t, repo, "diff", "--cached", "--name-only"); got != "" {
		t.Fatal("failed to restore identical file to index", got)
	}
}
