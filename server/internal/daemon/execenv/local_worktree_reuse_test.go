package execenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRetainedLocalWorktreeReusesPhysicalCheckoutAndReplaysUserChanges(t *testing.T) {
	repo := newTestRepo(t)
	params := LocalWorktreeParams{LocalPath: repo, EnvRoot: t.TempDir(), AgentName: "J", TaskID: turnOneTask, WorkspaceID: "ws", AgentID: "agent", ConversationID: "issue", ConversationKey: "MUL-42", RetainCheckout: true}
	first, err := PrepareLocalWorktree(params, worktreeTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(first.WorkDir, "agent.txt"), "agent delivery")
	if outcome, err := first.Finalize(worktreeTestLogger()); err != nil || outcome.Branch == "" {
		t.Fatalf("first delivery failed: %+v %v", outcome, err)
	}
	before, err := os.Stat(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "user.txt"), "new user edit")
	previous, err := ReadRetainedLocalWorktree(params.EnvRoot)
	if err != nil {
		t.Fatal(err)
	}
	params.TaskID = turnTwoTask
	second, err := ReuseLocalWorktree(previous, params, worktreeTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	published, err := ReadRetainedLocalWorktree(params.EnvRoot)
	if err != nil || published.userState != second.userState || published.BaseCommit != second.BaseCommit {
		t.Fatalf("concurrent readers would inherit a stale replay baseline: %+v %v", published, err)
	}
	after, err := os.Stat(second.Path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("physical checkout was rebuilt")
	}
	for file, want := range map[string]string{"agent.txt": "agent delivery", "user.txt": "new user edit"} {
		if data, err := os.ReadFile(filepath.Join(second.Path, file)); err != nil || string(data) != want {
			t.Fatalf("%s lost during reuse: %q %v", file, data, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(repo, "user.txt")); err != nil || string(data) != "new user edit" {
		t.Fatal("user checkout changed")
	}
	if _, err := os.Stat(filepath.Join(repo, "agent.txt")); !os.IsNotExist(err) {
		t.Fatal("agent delivery written into user checkout")
	}
	if _, err := second.Finalize(worktreeTestLogger()); err != nil {
		t.Fatal(err)
	}
	params.AgentID = "different-agent"
	if _, err := ReuseLocalWorktree(second, params, worktreeTestLogger()); err == nil {
		t.Fatal("another agent reused checkout")
	}
	params.AgentID = "agent"
	writeFile(t, filepath.Join(second.Path, "unsaved.txt"), "preserve unsaved changes")
	if _, err := ReuseLocalWorktree(second, params, worktreeTestLogger()); err == nil {
		t.Fatal("dirty retained checkout silently reused")
	}
	if data, err := os.ReadFile(filepath.Join(second.Path, "unsaved.txt")); err != nil || string(data) != "preserve unsaved changes" {
		t.Fatal("declined reuse lost unsaved work")
	}
}

func TestLocalWorktreeBranchesCannotContinueAcrossExecutionScopes(t *testing.T) {
	for _, change := range []string{"project", "squad", "runtime", "repository"} {
		t.Run(change, func(t *testing.T) {
			repo := newTestRepo(t)
			params := LocalWorktreeParams{LocalPath: repo, EnvRoot: t.TempDir(), AgentName: "J", TaskID: turnOneTask, WorkspaceID: "ws", AgentID: "agent", ConversationID: "issue", ConversationKey: "MUL-42", ProjectID: "project", SquadID: "squad", RuntimeID: "runtime", RepositoryScope: "scope"}
			first, err := PrepareLocalWorktree(params, worktreeTestLogger())
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(first.WorkDir, "delivery.txt"), "prior project delivery")
			if _, err := first.Finalize(worktreeTestLogger()); err != nil {
				t.Fatal(err)
			}
			params.EnvRoot, params.TaskID = t.TempDir(), turnTwoTask
			switch change {
			case "project":
				params.ProjectID = "other"
			case "squad":
				params.SquadID = "other"
			case "runtime":
				params.RuntimeID = "other"
			case "repository":
				params.RepositoryScope = "other"
			}
			second, err := PrepareLocalWorktree(params, worktreeTestLogger())
			if err != nil {
				t.Fatal(err)
			}
			defer second.Discard(worktreeTestLogger())
			if second.Continued || second.Branch == first.Branch {
				t.Fatalf("branch crossed %s scope", change)
			}
			if _, err := os.Stat(filepath.Join(second.Path, "delivery.txt")); !os.IsNotExist(err) {
				t.Fatalf("old %s code inherited", change)
			}
		})
	}
}
