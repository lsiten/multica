package localreview

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIndexTransactionStagesOnlySelectedFile(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "selected\n")
	write(t, repo, "other.txt", "leave unstaged\n")
	tx, err := openIndexTransaction(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	cmd := exec.Command("git", "-C", repo, "add", "--", "app.txt")
	cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+tx.scratchPath())
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if got := run(t, repo, "diff", "--cached", "--name-only"); got != "" {
		t.Fatal("scratch changed real index before publication", got)
	}
	if err := tx.publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := run(t, repo, "diff", "--cached", "--name-only"); got != "app.txt" {
		t.Fatal("wrong staged paths", got)
	}
}

func TestIndexTransactionRejectsCancellationWithoutPublishing(t *testing.T) {
	repo := repository(t)
	index := filepath.Join(repo, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := openIndexTransaction(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := tx.publish(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("expected cancellation", err)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(index)
	if err != nil || string(after) != string(before) {
		t.Fatal("cancelled transaction changed index", err)
	}
	if _, err := os.Stat(index + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled transaction retained lock", err)
	}
}

func TestIndexTransactionDoesNotOverwriteExternallyChangedIndex(t *testing.T) {
	repo := repository(t)
	tx, err := openIndexTransaction(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	index := filepath.Join(repo, ".git", "index")
	if err := os.WriteFile(index, []byte("external-change"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := tx.publish(t.Context()); !errors.Is(err, ErrIndexStateChanged) {
		t.Fatal("overwrote concurrent index change", err)
	}
	after, err := os.ReadFile(index)
	if err != nil || string(after) != "external-change" {
		t.Fatal("external index was lost", err)
	}
}

func TestIndexTransactionRespectsExistingGitLock(t *testing.T) {
	repo := repository(t)
	lock := filepath.Join(repo, ".git", "index.lock")
	if err := os.WriteFile(lock, []byte("other-operation"), 0600); err != nil {
		t.Fatal(err)
	}
	if tx, err := openIndexTransaction(t.Context(), repo); err == nil {
		tx.Close()
		t.Fatal("ignored existing Git lock")
	}
	contents, err := os.ReadFile(lock)
	if err != nil || string(contents) != "other-operation" {
		t.Fatal("modified somebody else's lock", err)
	}
}
