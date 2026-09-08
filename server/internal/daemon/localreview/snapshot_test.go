package localreview

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, path string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func repository(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	run(t, p, "init", "-b", "main")
	run(t, p, "config", "user.name", "Review Test")
	run(t, p, "config", "user.email", "review@example.test")
	write(t, p, "app.txt", "before\n")
	run(t, p, "add", ".")
	run(t, p, "commit", "-m", "base")
	run(t, p, "checkout", "-b", "feature")
	return p
}

func write(t *testing.T, path, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(path, name), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadIncludesCommittedStagedAndUntrackedWithoutMutatingGit(t *testing.T) {
	p := repository(t)
	write(t, p, "app.txt", "committed\n")
	run(t, p, "commit", "-am", "feature")
	write(t, p, "app.txt", "staged\n")
	run(t, p, "add", "app.txt")
	write(t, p, "new file.txt", "untracked content\n")
	before := run(t, p, "status", "--porcelain")
	s, err := Read(context.Background(), p, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Dirty || len(s.Files) != 2 || !strings.Contains(s.Commits, "feature") {
		t.Fatalf("unexpected snapshot: %+v", s)
	}
	if !strings.Contains(s.Files[0].Patch, "+staged") || !strings.Contains(s.Files[0].Patch, "-before") {
		t.Fatal("missing tracked diff")
	}
	if s.Files[1].Patch != "untracked content\n" {
		t.Fatal("missing untracked contents")
	}
	if run(t, p, "status", "--porcelain") != before {
		t.Fatal("read modified index or worktree")
	}
	write(t, p, "new file.txt", "changed\n")
	next, err := Read(context.Background(), p, "main")
	if err != nil || next.ID == s.ID {
		t.Fatal("snapshot did not invalidate after untracked edit", err)
	}
}

func TestReadRejectsInvalidTargetsAndSymlinks(t *testing.T) {
	p := repository(t)
	for _, target := range []string{"--all", "main~1", "missing"} {
		if _, err := Read(context.Background(), p, target); err == nil {
			t.Fatalf("accepted %q", target)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(p, "link")); err != nil {
		t.Skip(err)
	}
	if _, err := Read(context.Background(), p, "main"); err == nil {
		t.Fatal("followed untracked symlink")
	}
}

func TestReadDiscoversRepositoriesWithoutChoosingAmongMultiple(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	if err := os.Mkdir(first, 0o700); err != nil {
		t.Fatal(err)
	}
	run(t, first, "init", "-b", "main")
	run(t, first, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-m", "base")
	run(t, first, "checkout", "-b", "feature")
	single, err := Read(context.Background(), root, "main")
	if err != nil || single.Head == "" || filepath.Base(single.Path) != "first" {
		t.Fatal("single repo was not resolved", err)
	}
	second := filepath.Join(root, "second")
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}
	run(t, second, "init", "-b", "main")
	multi, err := Read(context.Background(), root, "main")
	if err != nil || len(multi.Repositories) != 2 || multi.Head != "" {
		t.Fatal("multiple repos were not exposed for selection", err)
	}
}
