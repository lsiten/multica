package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunTaskProjectSupervisionUsesNeutralDirectory(t *testing.T) {
	for _, mode := range []string{"in_place", "worktree"} {
		t.Run(mode, func(t *testing.T) {
			// Given a project with local changes and a remote repository.
			d, _, cleanup := newLeaderReuseTestDaemon(t)
			t.Cleanup(cleanup)
			d.cfg.DaemonID = "supervisor-daemon"
			d.localPathLocks = NewLocalPathLocker()
			d.workspaces["ws-leader"] = &workspaceState{workspaceID: "ws-leader"}
			repo := createWorktreeTestRepo(t)
			if err := os.WriteFile(filepath.Join(repo, "user-wip.txt"), []byte("uncommitted user work"), 0600); err != nil {
				t.Fatal(err)
			}
			before := worktreeTestGit(t, repo, "status", "--porcelain")
			branches := worktreeTestGit(t, repo, "for-each-ref", "--format=%(refname):%(objectname)")
			ref, err := json.Marshal(localDirectoryRef{LocalPath: repo, DaemonID: d.cfg.DaemonID, ExecutionMode: mode})
			if err != nil {
				t.Fatal(err)
			}
			task := leaderReuseTestTask("supervision-one")
			task.IsLeaderTask, task.IssueID = false, ""
			task.ProjectID, task.ProjectSupervisionPrompt = "project", "review project"
			task.ProjectResources = []ProjectResourceData{{ID: "resource", ResourceType: "local_directory", ResourceRef: ref}}
			task.Repos = []RepoData{{URL: "https://example.invalid/project.git"}}
			// When the real execution pipeline launches a test-created provider.
			result, err := d.runTask(context.Background(), task, "claude", 0, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			// Then it owns scratch space without taking a project checkout or snapshot.
			if filepath.Base(result.WorkDir) != "workdir" || sameDir(t, result.WorkDir, repo) || result.BranchName != "" || result.WorktreeCommit != "" {
				t.Fatalf("supervision acquired project code: workdir=%q branch=%q commit=%q", result.WorkDir, result.BranchName, result.WorktreeCommit)
			}
			if got := worktreeTestGit(t, repo, "status", "--porcelain"); got != before {
				t.Fatalf("changed user work: %q", got)
			}
			if got := worktreeTestGit(t, repo, "for-each-ref", "--format=%(refname):%(objectname)"); got != branches {
				t.Fatalf("created a baseline or branch: %q", got)
			}
			if len(d.workspaces[task.WorkspaceID].taskRepoURLs) != 0 {
				t.Fatal("supervision registered repositories for background checkout")
			}
			t.Logf("neutral cwd=%s, no project Git mutation, no remote checkout registration", result.WorkDir)
		})
	}
}

func TestProjectSupervisionCannotCheckoutRepository(t *testing.T) {
	// Given a live coordination credential, even if the URL is otherwise allowed.
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	t.Cleanup(cleanup)
	d.registerActiveRepoCheckoutTask("supervision-token", activeRepoCheckoutTask{CoordinationOnly: true, WorkspaceID: "ws-leader", TaskID: "supervision", WorkDir: t.TempDir()})
	req := httptest.NewRequest(http.MethodPost, "/repo/checkout", strings.NewReader(`{"url":"https://example.invalid/project.git"}`))
	req.Header.Set("Authorization", "Bearer supervision-token")
	response := httptest.NewRecorder()
	// When the coordinator requests a code checkout.
	d.repoCheckoutHandler().ServeHTTP(response, req)
	// Then deny before resolving or creating repository storage.
	if response.Code != http.StatusForbidden {
		t.Fatalf("checkout status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRunTaskProjectSupervisionRejectsConflictingIdentityBeforePreparation(t *testing.T) {
	for _, tc := range []struct {
		name string
		bind func(*Task)
	}{
		{"shared issue", func(task *Task) { task.CodeWorkspaceIssueID = "shared-issue" }},
		{"shared identifier", func(task *Task) { task.CodeWorkspaceIssueIdentifier = "ISSUE-1" }},
		{"shared directory", func(task *Task) { task.CodeWorkspaceWorkDir = "/unopened/project" }},
		{"issue", func(task *Task) { task.IssueID = "issue" }},
		{"chat", func(task *Task) { task.ChatSessionID = "chat" }},
		{"autopilot", func(task *Task) { task.AutopilotID = "autopilot" }},
		{"autopilot run", func(task *Task) { task.AutopilotRunID = "autopilot-run" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a coordination payload with an incompatible execution identity.
			d, argsFile, cleanup := newLeaderReuseTestDaemon(t)
			t.Cleanup(cleanup)
			task := leaderReuseTestTask("conflicting-supervision")
			task.IsLeaderTask, task.IssueID = false, ""
			task.ProjectID, task.ProjectSupervisionPrompt = "project", "review project"
			tc.bind(&task)
			// When the task enters execution.
			_, err := d.runTask(t.Context(), task, "claude", 0, d.logger)
			// Then reject before any workspace or provider side effect.
			if !errors.Is(err, errInvalidTaskIdentity) {
				t.Fatalf("wanted invalid task identity, got %v", err)
			}
			entries, err := os.ReadDir(d.cfg.WorkspacesRoot)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("invalid identity wrote workspace entries: %v", entries)
			}
			if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
				t.Fatalf("invalid identity invoked provider: %v", err)
			}
		})
	}
}
