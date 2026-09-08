package localreview

import (
	"context"
	"strings"
	"testing"
)

func TestCommittedReviewExcludesLaterWorkingDirectoryEdits(t *testing.T) {
	p := repository(t)
	write(t, p, "app.txt", "delivered\n")
	run(t, p, "commit", "-am", "delivery")
	head := run(t, p, "rev-parse", "HEAD")
	run(t, p, "checkout", "main")
	write(t, p, "app.txt", "private later edit\n")
	s, err := ReadCommitted(context.Background(), CommittedRequest{Path: p, Branch: "feature", Head: head, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Committed || s.Dirty || len(s.Files) != 1 {
		t.Fatal("wrong committed snapshot")
	}
	if !strings.Contains(s.Files[0].Patch, "+delivered") || strings.Contains(s.Files[0].Patch, "private later edit") {
		t.Fatal("diff did not use pinned source")
	}
	write(t, p, "app.txt", "another private edit\n")
	next, err := ReadCommitted(context.Background(), CommittedRequest{Path: p, Branch: "feature", Head: head, Target: "main"})
	if err != nil || next.ID != s.ID {
		t.Fatal("working directory edits changed committed review", err)
	}
	if _, err := Merge(context.Background(), s); err == nil {
		t.Fatal("merged into dirty target")
	}
	run(t, p, "restore", "app.txt")
	commit, err := Merge(context.Background(), s)
	if err != nil || run(t, p, "rev-parse", "HEAD") != commit {
		t.Fatal("committed review did not merge", err)
	}
}
