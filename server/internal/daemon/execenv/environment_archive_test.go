package execenv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentArchiveRoundTripGitStates(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, kind := range []string{"unborn", "detached", "split_index", "conflict", "other_branch"} {
		t.Run(kind, func(t *testing.T) {
			workspace := t.TempDir()
			root := filepath.Join(workspace, "ws", "task")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := writeEnvRootOwner(root, "workspace", "task"); err != nil {
				t.Fatal(err)
			}
			repo := filepath.Join(root, "workdir")
			if kind == "unborn" {
				if err := os.Mkdir(repo, 0700); err != nil {
					t.Fatal(err)
				}
				gitRun(t, repo, "init", "-b", "main")
				if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("staged"), 0644); err != nil {
					t.Fatal(err)
				}
				gitRun(t, repo, "add", "new.txt")
			} else {
				source := newTestRepo(t)
				gitRun(t, source, "clone", "--no-hardlinks", source, repo)
				gitRun(t, repo, "config", "user.useConfigOnly", "true")
				switch kind {
				case "detached":
					gitRun(t, repo, "checkout", "--detach", "HEAD")
				case "split_index":
					gitRun(t, repo, "update-index", "--split-index")
				case "conflict":
					gitRun(t, repo, "checkout", "-b", "side")
					if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("side\n"), 0644); err != nil {
						t.Fatal(err)
					}
					gitRun(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-am", "side")
					gitRun(t, repo, "checkout", "main")
					if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("main\n"), 0644); err != nil {
						t.Fatal(err)
					}
					gitRun(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-am", "main")
					if _, err := gitTry(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.test", "merge", "side"); err == nil {
						t.Fatal("expected fixture conflict")
					}
				case "other_branch":
					gitRun(t, repo, "checkout", "-b", "unpublished")
					gitRun(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-m", "unpublished")
					gitRun(t, repo, "checkout", "main")
				}
			}
			before := gitRun(t, repo, "status", "--porcelain", "--untracked-files=all")
			stages := gitRun(t, repo, "ls-files", "--stage")
			refs := gitRun(t, repo, "for-each-ref", "--format=%(refname) %(objectname)")
			var mergeHead []byte
			if kind == "conflict" {
				var err error
				mergeHead, err = os.ReadFile(filepath.Join(repo, ".git", "MERGE_HEAD"))
				if err != nil || strings.TrimSpace(string(mergeHead)) == "" {
					t.Fatalf("fixture did not enter merge conflict state: %q %v", mergeHead, err)
				}
			}
			revision, err := EnvironmentArchiveRevision(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			id := strings.Repeat("c", 64)
			if _, err := CaptureEnvironmentArchive(t.Context(), EnvironmentArchiveRequest{WorkspacesRoot: workspace, EnvRoot: root, ID: id, Revision: revision}); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			if _, err := RestoreEnvironmentArchive(t.Context(), workspace, id, "", ""); err != nil {
				t.Fatal(err)
			}
			if actual := gitRun(t, repo, "status", "--porcelain", "--untracked-files=all"); actual != before {
				t.Fatalf("working state changed: %q != %q", actual, before)
			}
			if actual := gitRun(t, repo, "ls-files", "--stage"); actual != stages {
				t.Fatalf("index stages changed: %q != %q", actual, stages)
			}
			if actual := gitRun(t, repo, "for-each-ref", "--format=%(refname) %(objectname)"); actual != refs {
				t.Fatalf("refs changed: %q != %q", actual, refs)
			}
			if kind == "conflict" {
				actual, err := os.ReadFile(filepath.Join(repo, ".git", "MERGE_HEAD"))
				if err != nil || string(actual) != string(mergeHead) {
					t.Fatalf("merge continuation state lost: %q %v", actual, err)
				}
			}
		})
	}
}

func TestEnvironmentArchiveRejectsCorruptionAndExistingEmptyDestination(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "ws", "task")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeEnvRootOwner(root, "workspace", "task"); err != nil {
		t.Fatal(err)
	}
	revision, err := EnvironmentArchiveRevision(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("d", 64)
	if _, err := CaptureEnvironmentArchive(t.Context(), EnvironmentArchiveRequest{WorkspacesRoot: workspace, EnvRoot: root, ID: id, Revision: revision}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreEnvironmentArchive(t.Context(), workspace, id, "", ""); err == nil {
		t.Fatal("empty destination was replaced")
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(workspace, environmentArchiveDir, id, "environment.tar.gz")
	file, err := os.OpenFile(payload, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("corrupt"), 0); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreEnvironmentArchive(t.Context(), workspace, id, "", ""); err == nil {
		t.Fatal("corrupt snapshot was restored")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("corruption published a partial restore: %v", err)
	}
}

func TestEnvironmentArchiveRestoresLinkedWorktreeWithoutOriginalRepository(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "ws", "task")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeEnvRootOwner(root, "workspace", "task"); err != nil {
		t.Fatal(err)
	}
	source := newTestRepo(t)
	checkout := filepath.Join(root, "worktree")
	gitRun(t, source, "worktree", "add", "-b", "agent/archive", checkout)
	gitRun(t, checkout, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-m", "unpushed history")
	head := gitRun(t, checkout, "rev-parse", "HEAD")
	write := func(relative, content string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("worktree/tracked.txt", "staged\n", 0644)
	gitRun(t, checkout, "add", "tracked.txt")
	write("worktree/tracked.txt", "unstaged\n", 0644)
	write("worktree/untracked.sh", "#!/bin/sh\nexit 0\n", 0755)
	write("output/report.txt", "result", 0600)
	write("logs/run.log", "log", 0600)
	write("codex-home/session.json", "session", 0600)
	write("codex-home/.sandbox-bin/binary", "regenerable", 0700)
	status := gitRun(t, checkout, "status", "--porcelain", "--untracked-files=all")
	staged := gitRun(t, checkout, "diff", "--cached")
	revision, err := EnvironmentArchiveRevision(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := CaptureEnvironmentArchive(t.Context(), EnvironmentArchiveRequest{
		WorkspacesRoot: workspace, EnvRoot: root, ID: strings.Repeat("a", 64), Profile: "test", Revision: revision,
		ExcludedCaches: []string{"codex-home/.sandbox-bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if archive.Owner.WorkspaceID != "workspace" || archive.Owner.TaskID != "task" || archive.PayloadBytes <= 0 {
		t.Fatalf("invalid archive: %+v", archive)
	}
	if err := VerifyEnvironmentArchive(t.Context(), workspace, archive.ID); err != nil {
		t.Fatal(err)
	}
	release, err := LockEnvironmentArchiveRepositories(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReclaimEnvironmentArchive(t.Context(), workspace, archive.ID); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreEnvironmentArchive(t.Context(), workspace, archive.ID, "test", ""); err != nil {
		t.Fatal(err)
	}
	if restored := gitRun(t, checkout, "rev-parse", "HEAD"); restored != head {
		t.Fatalf("unpushed commit lost: %s != %s", restored, head)
	}
	if restored := gitRun(t, checkout, "status", "--porcelain", "--untracked-files=all"); restored != status {
		t.Fatalf("working state changed:\n%s\n!=\n%s", restored, status)
	}
	if restored := gitRun(t, checkout, "diff", "--cached"); restored != staged {
		t.Fatalf("staged changes lost:\n%s\n!=\n%s", restored, staged)
	}
	for _, relative := range []string{"output/report.txt", "logs/run.log", "codex-home/session.json"} {
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatalf("retained resource %s lost: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "codex-home/.sandbox-bin")); !os.IsNotExist(err) {
		t.Fatalf("regenerable cache restored: %v", err)
	}
	if _, err := RestoreEnvironmentArchive(t.Context(), workspace, archive.ID, "test", ""); err == nil {
		t.Fatal("restore overwrote an existing environment")
	}
}

func TestEnvironmentArchiveRejectsChangedRevisionAndCancellation(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "ws", "task")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeEnvRootOwner(root, "workspace", "task"); err != nil {
		t.Fatal(err)
	}
	revision, err := EnvironmentArchiveRevision(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "result"), []byte("new work"), 0600); err != nil {
		t.Fatal(err)
	}
	request := EnvironmentArchiveRequest{WorkspacesRoot: workspace, EnvRoot: root, ID: strings.Repeat("b", 64), Revision: revision}
	if _, err := CaptureEnvironmentArchive(t.Context(), request); err == nil {
		t.Fatal("stale archive preview accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CaptureEnvironmentArchive(ctx, request); err == nil {
		t.Fatal("cancelled archive accepted")
	}
	if data, err := os.ReadFile(filepath.Join(root, "result")); err != nil || string(data) != "new work" {
		t.Fatalf("source changed on failure: %q %v", data, err)
	}
}
