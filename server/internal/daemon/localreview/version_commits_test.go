package localreview

import (
	"testing"
)

func TestVersionCommitPagesRemainPinnedAfterSourceAdvances(t *testing.T) {
	repo := repository(t)
	for _, name := range []string{"first", "second", "third"} {
		write(t, repo, "app.txt", name+"\n")
		run(t, repo, "commit", "-am", name)
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
	write(t, repo, "app.txt", "later\n")
	run(t, repo, "commit", "-am", "not in captured version")
	first, err := version.CommitPage(t.Context(), FilePageRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Commits) != 2 || !first.HasMore || first.Commits[0].Subject != "third" || first.NextOffset != 2 {
		t.Fatalf("wrong first page: %+v", first)
	}
	second, err := version.CommitPage(t.Context(), FilePageRequest{Offset: first.NextOffset, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Commits) != 1 || second.HasMore || second.Commits[0].Subject != "first" {
		t.Fatalf("wrong second page: %+v", second)
	}
}
