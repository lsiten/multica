package execenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPrepareLocalWorktreeForksWhenBranchErrorIsLocalized(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("localized Git diagnostic fixture requires a POSIX shell")
	}
	repo := newTestRepo(t)
	first := prepareTurn(t, repo, "LSIT-161", turnOneTask)
	writeFile(t, filepath.Join(first.Path, "turn-one.txt"), "committed work\n")
	gitRun(t, first.Path, "add", "turn-one.txt")
	gitRun(t, first.Path, "commit", "-m", "turn one")
	tip := gitRun(t, first.Path, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(first.Path, "tracked.txt"), "staged work\n")
	gitRun(t, first.Path, "add", "tracked.txt")
	writeFile(t, filepath.Join(first.Path, "tracked.txt"), "unstaged work\n")
	writeFile(t, filepath.Join(first.Path, "draft.txt"), "untracked work\n")
	status := gitRun(t, first.Path, "status", "--porcelain", "--untracked-files=all")

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	quotedGit := "'" + strings.ReplaceAll(gitPath, "'", "'\\''") + "'"
	bin := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
  *"worktree add"*)
    output=$(%s "$@" 2>&1)
    result=$?
    if [ "$result" -ne 0 ]; then
      printf '致命错误：分支已经被工作区使用\n' >&2
      exit "$result"
    fi
    printf '%%s\n' "$output"
    exit 0
    ;;
esac
exec %s "$@"
`, quotedGit, quotedGit)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	sibling := prepareTurn(t, repo, "LSIT-161", turnTwoTask)
	if sibling.Branch == first.Branch || sibling.BaseCommit != tip {
		t.Fatalf("new checkout branch/base = %s/%s, occupied branch/tip = %s/%s", sibling.Branch, sibling.BaseCommit, first.Branch, tip)
	}
	if got := readFile(t, filepath.Join(sibling.Path, "turn-one.txt")); got != "committed work\n" {
		t.Fatalf("new checkout lost the existing branch's work: %q", got)
	}
	if got := gitRun(t, first.Path, "status", "--porcelain", "--untracked-files=all"); got != status {
		t.Fatalf("occupied checkout status changed: got %q, want %q", got, status)
	}
	if got := gitRun(t, first.Path, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("occupied checkout HEAD changed: got %s, want %s", got, tip)
	}
	if got := readFile(t, filepath.Join(first.Path, "tracked.txt")); got != "unstaged work\n" {
		t.Fatalf("occupied checkout contents changed: %q", got)
	}
	if got := gitRun(t, first.Path, "show", ":tracked.txt"); got != "staged work" {
		t.Fatalf("occupied checkout staged contents changed: %q", got)
	}
	if got := readFile(t, filepath.Join(first.Path, "draft.txt")); got != "untracked work\n" {
		t.Fatalf("occupied checkout untracked file changed: %q", got)
	}
	finalizeOK(t, sibling)
}

func TestPrepareLocalWorktreeForksWhenFallbackBranchIsBusy(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	first := prepareTurn(t, repo, "LSIT-161", turnOneTask)
	writeFile(t, filepath.Join(first.Path, "turn-one.txt"), "committed work\n")
	gitRun(t, first.Path, "add", "turn-one.txt")
	gitRun(t, first.Path, "commit", "-m", "turn one")
	tip := gitRun(t, first.Path, "rev-parse", "HEAD")
	previous := prepareTurn(t, repo, "LSIT-161", turnTwoTask)
	writeFile(t, filepath.Join(previous.Path, "draft.txt"), "retained work\n")
	status := gitRun(t, previous.Path, "status", "--porcelain", "--untracked-files=all")

	retry := prepareTurn(t, repo, "LSIT-161", turnTwoTask)
	if !strings.HasPrefix(retry.Branch, previous.Branch+"-") || retry.BaseCommit != tip {
		t.Fatalf("retry branch/base = %s/%s, want a new branch from %s", retry.Branch, retry.BaseCommit, tip)
	}
	if got := readFile(t, filepath.Join(retry.Path, "turn-one.txt")); got != "committed work\n" {
		t.Fatalf("retry lost the conversation branch's work: %q", got)
	}
	if got := gitRun(t, previous.Path, "status", "--porcelain", "--untracked-files=all"); got != status {
		t.Fatalf("previous checkout status changed: got %q, want %q", got, status)
	}
	if got := readFile(t, filepath.Join(previous.Path, "draft.txt")); got != "retained work\n" {
		t.Fatalf("previous checkout contents changed: %q", got)
	}
	finalizeOK(t, retry)
}

func TestAddLocalWorktreeDoesNotForkForPathFailure(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	branch := "agent/j/lsit-161"
	gitRun(t, repo, "branch", branch)
	tip := gitRun(t, repo, "rev-parse", branch)
	path := t.TempDir()
	writeFile(t, filepath.Join(path, "protected.txt"), "existing directory\n")
	branches := gitRun(t, repo, "for-each-ref", "--format=%(refname)", "refs/heads/")

	actual, created, err := addLocalWorktree(repo, path, taskBranchPlan{name: branch, base: tip, continues: true, conversational: true}, turnTwoTask)
	if err == nil || actual != "" || created {
		t.Fatalf("path failure = (%q, %v, %v), want no checkout", actual, created, err)
	}
	if got := gitRun(t, repo, "for-each-ref", "--format=%(refname)", "refs/heads/"); got != branches {
		t.Fatalf("path failure created unrelated branches: got %q, want %q", got, branches)
	}
	if got := readFile(t, filepath.Join(path, "protected.txt")); got != "existing directory\n" {
		t.Fatalf("path failure changed existing contents: %q", got)
	}
}
