package localreview

import (
	"errors"
	"strings"
	"testing"
)

func TestSelectedMergeExcludesUnselectedChangesAndPreservesTargetEdits(t *testing.T) {
	repo := repository(t)
	write(t, repo, "other.txt", "base\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-m", "shared base")
	run(t, repo, "branch", "-f", "main", "HEAD")
	write(t, repo, "app.txt", "selected source\n")
	write(t, repo, "other.txt", "unselected source\n")
	run(t, repo, "commit", "-am", "source changes")
	source := run(t, repo, "rev-parse", "HEAD")
	target := t.TempDir()
	run(t, repo, "worktree", "add", target, "main")
	write(t, target, "other.txt", "target edit\n")
	run(t, target, "commit", "-am", "target change")
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureCommitted(t.Context(), CommittedRequest{Path: repo, Branch: "feature", Head: source, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	prepared := ""
	commit, err := store.MergeSelectedPrepared(t.Context(), SelectedMergeRequest{Version: VersionSelection{ID: id, Path: version.Header.Repository, Target: "main"}, Paths: []string{"app.txt"}, Message: "apply selected file"}, func(commit string) error { prepared = commit; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if commit == "" || prepared != commit || run(t, target, "rev-parse", "HEAD") != commit {
		t.Fatal("missing prepared publication")
	}
	if run(t, target, "show", "HEAD:app.txt") != "selected source" {
		t.Fatal("selected change missing")
	}
	if run(t, target, "show", "HEAD:other.txt") != "target edit" {
		t.Fatal("unselected source overwrote target")
	}
	if run(t, repo, "rev-parse", "HEAD") != source {
		t.Fatal("source branch moved")
	}
	if run(t, target, "rev-parse", "HEAD^1") != version.Header.TargetHead {
		t.Fatal("target history was rewritten")
	}
	if len(strings.Fields(run(t, target, "rev-list", "--parents", "-n", "1", commit))) != 2 {
		t.Fatal("selective merge falsely marked source history merged")
	}
}

func TestSelectedMergeConflictLeavesTargetUnchanged(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "source conflict\n")
	run(t, repo, "commit", "-am", "source")
	source := run(t, repo, "rev-parse", "HEAD")
	target := t.TempDir()
	run(t, repo, "worktree", "add", target, "main")
	write(t, target, "app.txt", "target conflict\n")
	run(t, target, "commit", "-am", "target")
	head := run(t, target, "rev-parse", "HEAD")
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureCommitted(t.Context(), CommittedRequest{Path: repo, Branch: "feature", Head: source, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	prepared := false
	_, err = store.MergeSelectedPrepared(t.Context(), SelectedMergeRequest{Version: VersionSelection{ID: id, Path: version.Header.Repository, Target: "main"}, Paths: []string{"app.txt"}, Message: "must conflict"}, func(string) error { prepared = true; return nil })
	if !errors.Is(err, ErrSelectedMergeConflict) || prepared {
		t.Fatal("conflict was not stopped before preparation", err)
	}
	var conflict *SelectedMergeConflictError
	if !errors.As(err, &conflict) || len(conflict.Files) != 1 || conflict.Files[0] != "app.txt" {
		t.Fatalf("conflict paths missing: %v", err)
	}
	if run(t, target, "rev-parse", "HEAD") != head || run(t, target, "show", "HEAD:app.txt") != "target conflict" {
		t.Fatal("conflict modified target")
	}
}
