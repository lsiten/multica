package localreview

import (
	"errors"
	"testing"
)

func TestIndexCommitIncludesOnlyStagedBytes(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "staged contents\n")
	run(t, repo, "add", "app.txt")
	write(t, repo, "app.txt", "newer unstaged contents\n")
	write(t, repo, "other.txt", "untracked\n")
	status, err := ReadIndexStatus(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	var prepared PreparedIndexCommit
	commit, err := CommitIndexPrepared(t.Context(), IndexCommitRequest{Path: repo, Branch: status.Branch, Head: status.Head, IndexID: status.IndexID, Message: "selected staged commit"}, func(value PreparedIndexCommit) error {
		prepared = value
		if got := run(t, repo, "rev-parse", "HEAD"); got != status.Head {
			t.Fatal("HEAD moved before preparation")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit == "" || prepared.Commit != commit || run(t, repo, "rev-parse", "HEAD") != commit {
		t.Fatal("commit/result mismatch")
	}
	if got := run(t, repo, "show", "HEAD:app.txt"); got != "staged contents" {
		t.Fatal("committed unstaged bytes", got)
	}
	if got := run(t, repo, "diff", "--name-only"); got != "app.txt" {
		t.Fatal("lost unstaged edits", got)
	}
	if got := run(t, repo, "diff", "--cached", "--name-only"); got != "" {
		t.Fatal("index does not match committed tree", got)
	}
}

func TestIndexCommitDoesNotAdvanceWhenReceiptPreparationFails(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "staged\n")
	run(t, repo, "add", "app.txt")
	status, err := ReadIndexStatus(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("receipt unavailable")
	_, err = CommitIndexPrepared(t.Context(), IndexCommitRequest{Path: repo, Branch: status.Branch, Head: status.Head, IndexID: status.IndexID, Message: "must not publish"}, func(PreparedIndexCommit) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatal("preparation failure was hidden", err)
	}
	if run(t, repo, "rev-parse", "HEAD") != status.Head {
		t.Fatal("HEAD advanced without receipt")
	}
}

func TestIndexCommitRejectsEmptyStagingWithoutPreparingReceipt(t *testing.T) {
	repo := repository(t)
	status, err := ReadIndexStatus(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	prepared := false
	_, err = CommitIndexPrepared(t.Context(), IndexCommitRequest{Path: repo, Branch: status.Branch, Head: status.Head, IndexID: status.IndexID, Message: "empty"}, func(PreparedIndexCommit) error { prepared = true; return nil })
	if !errors.Is(err, ErrNothingStaged) || prepared {
		t.Fatal("empty commit was prepared", err)
	}
}

func TestIndexCommitDoesNotOverwriteConcurrentBranchChange(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "staged\n")
	run(t, repo, "add", "app.txt")
	status, err := ReadIndexStatus(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	other := run(t, repo, "commit-tree", status.Head+"^{tree}", "-p", status.Head, "-m", "concurrent")
	_, err = CommitIndexPrepared(t.Context(), IndexCommitRequest{Path: repo, Branch: status.Branch, Head: status.Head, IndexID: status.IndexID, Message: "must not replace concurrent"}, func(PreparedIndexCommit) error {
		run(t, repo, "update-ref", "refs/heads/feature", other, status.Head)
		return nil
	})
	if !errors.Is(err, ErrIndexStateChanged) {
		t.Fatal("concurrent branch was not rejected", err)
	}
	if got := run(t, repo, "rev-parse", "HEAD"); got != other {
		t.Fatal("concurrent commit was overwritten", got)
	}
}
