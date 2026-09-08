package localreview

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestMergeStopsBeforeRefWriteWhenJournalCannotBeSaved(t *testing.T) {
	p := repository(t)
	write(t, p, "app.txt", "feature\n")
	run(t, p, "commit", "-am", "feature")
	s, err := Read(context.Background(), p, "main")
	if err != nil {
		t.Fatal(err)
	}
	_, err = MergePrepared(context.Background(), s, func(string) error { return errors.New("disk full") })
	if err == nil {
		t.Fatal("merged without journal")
	}
	if run(t, p, "rev-parse", "main") != s.TargetHead {
		t.Fatal("target moved despite journal failure")
	}
}

func TestCheckedOutTargetRejectsRewindAfterValidation(t *testing.T) {
	p := repository(t)
	base := run(t, p, "rev-parse", "main")
	target := filepath.Join(t.TempDir(), "target")
	run(t, p, "worktree", "add", target, "main")
	run(t, target, "commit", "--allow-empty", "-m", "target advance")
	write(t, p, "app.txt", "feature\n")
	run(t, p, "commit", "-am", "feature")
	s, err := Read(context.Background(), p, "main")
	if err != nil {
		t.Fatal(err)
	}
	prepared := ""
	_, err = MergePrepared(context.Background(), s, func(commit string) error { prepared = commit; return errors.New("pause before ref update") })
	if err == nil || prepared == "" {
		t.Fatal("did not prepare merge")
	}
	// Simulate an external Git process changing the ref after preflight.
	run(t, target, "update-ref", "refs/heads/main", base, s.TargetHead)
	if err := updateCheckedOutTarget(context.Background(), p, target, s, prepared); err == nil {
		t.Fatal("overwrote concurrently rewound target")
	}
	if run(t, target, "rev-parse", "HEAD") != base {
		t.Fatal("target moved despite stale expected head")
	}
	if run(t, target, "status", "--porcelain") != "" {
		t.Fatal("stale update changed target files")
	}
}

func TestMergeUpdatesTargetCheckoutAndPreservesSource(t *testing.T) {
	p := repository(t)
	write(t, p, "app.txt", "feature\n")
	run(t, p, "commit", "-am", "feature")
	target := filepath.Join(t.TempDir(), "target")
	run(t, p, "worktree", "add", target, "main")
	s, err := Read(context.Background(), p, "main")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := Merge(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if run(t, target, "rev-parse", "HEAD") != commit {
		t.Fatal("target not updated")
	}
	if run(t, p, "rev-parse", "HEAD") != s.Head {
		t.Fatal("source moved")
	}
	if run(t, target, "status", "--porcelain") != "" {
		t.Fatal("dirty target")
	}
}

func TestMergeRejectsStaleAndDirtySnapshots(t *testing.T) {
	p := repository(t)
	write(t, p, "app.txt", "feature\n")
	run(t, p, "commit", "-am", "feature")
	s, err := Read(context.Background(), p, "main")
	if err != nil {
		t.Fatal(err)
	}
	write(t, p, "app.txt", "new\n")
	if _, err := Merge(context.Background(), s); err == nil {
		t.Fatal("merged unreviewed edit")
	}
	dirty, err := Read(context.Background(), p, "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(context.Background(), dirty); err == nil {
		t.Fatal("merged dirty source")
	}
	if run(t, p, "rev-parse", "main") != s.TargetHead {
		t.Fatal("target moved")
	}
}

func TestMergeConflictLeavesBothBranchesUntouched(t *testing.T) {
	p := repository(t)
	write(t, p, "app.txt", "feature\n")
	run(t, p, "commit", "-am", "feature")
	run(t, p, "checkout", "main")
	write(t, p, "app.txt", "other\n")
	run(t, p, "commit", "-am", "other")
	run(t, p, "checkout", "feature")
	s, err := Read(context.Background(), p, "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(context.Background(), s); err == nil {
		t.Fatal("merged conflicting changes")
	}
	if run(t, p, "rev-parse", "main") != s.TargetHead || run(t, p, "rev-parse", "HEAD") != s.Head {
		t.Fatal("branches changed")
	}
	if run(t, p, "status", "--porcelain") != "" {
		t.Fatal("conflict polluted checkout")
	}
}
