package execenv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestColdLocalWorktreeReattachesAndDeliversNextCommit(t *testing.T) {
	for _, mode := range []string{"reattach", "branch_changed", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			repo := newTestRepo(t)
			workspace := t.TempDir()
			params := PrepareParams{WorkspacesRoot: workspace, WorkspaceID: "workspace", TaskID: "first", RuntimeID: "runtime", Provider: "claude", AgentName: "J", IssueIdentifier: "MUL-42", Task: TaskContextForEnv{AgentID: "agent", IssueID: "issue"}, LocalWorktree: &LocalWorktreeParams{LocalPath: repo, RetainCheckout: true}}
			env, err := Prepare(params, worktreeTestLogger())
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(env.WorkDir, "first.txt"), "first delivery")
			if err := CleanupRuntimeConfig(env.WorkDir, "claude"); err != nil {
				t.Fatal(err)
			}
			if err := CleanupSidecars(env.RootDir); err != nil {
				t.Fatal(err)
			}
			if _, err := env.LocalWorktree.Finalize(worktreeTestLogger()); err != nil {
				t.Fatal(err)
			}
			env.ReleaseLock()
			revision, err := EnvironmentArchiveRevision(t.Context(), env.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			id := strings.Repeat("a", 64)
			if _, err := CaptureEnvironmentArchive(t.Context(), EnvironmentArchiveRequest{WorkspacesRoot: workspace, EnvRoot: env.RootDir, ID: id, Revision: revision}); err != nil {
				t.Fatal(err)
			}
			if err := ReclaimEnvironmentArchive(t.Context(), workspace, id); err != nil {
				t.Fatal(err)
			}
			if _, err := RestoreEnvironmentArchive(t.Context(), workspace, id, "", ""); err != nil {
				t.Fatal(err)
			}
			previous, err := ReadRetainedLocalWorktree(env.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			next := LocalWorktreeParams{LocalPath: repo, EnvRoot: env.RootDir, AgentName: "J", TaskID: "second", ConversationKey: "MUL-42", ConversationID: "issue", WorkspaceID: "workspace", AgentID: "agent", RuntimeID: "runtime", RetainCheckout: true}
			next.RepositoryScope, err = RepositoryScopeFingerprint(nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "branch_changed" {
				gitRun(t, repo, "update-ref", "refs/heads/"+previous.Branch, gitRun(t, repo, "rev-parse", "HEAD"))
			}
			ctx := t.Context()
			if mode == "cancelled" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			attachErr := ReattachRestoredLocalWorktree(ctx, previous, next, worktreeTestLogger())
			if mode != "reattach" {
				if attachErr == nil {
					t.Fatal("changed/cancelled worktree was reattached")
				}
				info, err := os.Lstat(filepath.Join(previous.Path, ".git"))
				if err != nil || !info.IsDir() {
					t.Fatal("self-contained recovery was not retained")
				}
				if data, err := os.ReadFile(filepath.Join(previous.Path, "first.txt")); err != nil || string(data) != "first delivery" {
					t.Fatal("recovery code lost")
				}
				return
			}
			if err := attachErr; err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(filepath.Join(previous.Path, ".git"))
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("restored checkout not linked: %v", err)
			}
			continued, err := ReuseLocalWorktree(previous, next, worktreeTestLogger())
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(continued.Path, "second.txt"), "second delivery")
			outcome, err := continued.Finalize(worktreeTestLogger())
			if err != nil {
				t.Fatal(err)
			}
			for path, want := range map[string]string{"first.txt": "first delivery", "second.txt": "second delivery"} {
				if got := gitRun(t, repo, "show", outcome.Branch+":"+path); got != want {
					t.Fatalf("%s missing from original branch: %q", path, got)
				}
				if _, err := os.Stat(filepath.Join(repo, path)); !os.IsNotExist(err) {
					t.Fatal("user checkout was modified")
				}
			}
		})
	}
}
