package daemon

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestCheckoutReviewSharesDirectoryStateAcrossTaskAndResources(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	d.workspaces = map[string]*workspaceState{"ws": {runtimeIDs: []string{"runtime"}}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws", "first", nil)
	repo := createWorktreeTestRepo(t)
	checkout := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", checkout)
	if err := os.WriteFile(filepath.Join(checkout, "current.txt"), []byte("current checkout\n"), 0600); err != nil {
		t.Fatal(err)
	}
	second := createTaskDir(t, d.cfg.WorkspacesRoot, "ws", "second", &execenv.GCMeta{LocalDirectory: true})
	if err := execenv.WriteReviewDirectory(second, execenv.ReviewDirectory{WorkspaceID: "ws", TaskID: "second", Path: checkout}); err != nil {
		t.Fatal(err)
	}
	var version string
	for _, locator := range []string{"first", "second", "resource-a", "resource-b"} {
		command := protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws", Path: checkout, Scope: "checkout", Action: "index"}
		if locator == "first" || locator == "second" {
			command.TaskID = locator
		} else {
			command.ProjectResourceID = locator
			command.DirectoryRoot = checkout
		}
		if version != "" {
			shared := command
			shared.Action, shared.VersionID, shared.FilePath, shared.Target = "content", version, "current.txt", "feature"
			if result := d.runRemoteReview(t.Context(), shared); result.Error != "" {
				t.Fatal("existing checkout version not shared across locators", locator, result.Error)
			}
		}
		result := d.runRemoteReview(t.Context(), command)
		if result.Error != "" {
			t.Fatal(locator, result.Error)
		}
		var index localIndexView
		if err := json.Unmarshal(result.Page, &index); err != nil {
			t.Fatal(err)
		}
		if len(index.Status.Files) != 1 || index.Status.Files[0].Path != "current.txt" {
			t.Fatalf("wrong checkout state for %s: %+v", locator, index)
		}
		if version == "" {
			version = index.VersionID
		} else if version != index.VersionID {
			t.Fatal("directory state depends on execution locator")
		}
		command.Action, command.VersionID, command.FilePath, command.Target = "content", version, "current.txt", "feature"
		result = d.runRemoteReview(t.Context(), command)
		if result.Error != "" {
			t.Fatal("shared version unreadable", locator, result.Error)
		}
	}
	request := worktreeReviewRequest{RuntimeID: "runtime", WorkspaceID: "ws", TaskID: "first", Path: checkout, Scope: "checkout", Action: "index"}
	if w := callReview(t, d, request, "test-token"); w.Code != http.StatusOK {
		t.Fatal("loopback checkout", w.Code, w.Body.String())
	}
	if err := os.WriteFile(filepath.Join(checkout, "current.txt"), []byte("changed by later execution\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result := d.runRemoteReview(t.Context(), protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws", TaskID: "first", Path: checkout, Scope: "checkout", Action: "index"})
	var changed localIndexView
	if err := json.Unmarshal(result.Page, &changed); err != nil {
		t.Fatal(err, result.Error)
	}
	if changed.VersionID == version {
		t.Fatal("earlier locator returned historical execution content")
	}
}

func TestCheckoutProjectReadRejectsSymlinkEscapeAndParentRepository(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	bound := t.TempDir()
	outside := createWorktreeTestRepo(t)
	escape := filepath.Join(bound, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(outside, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ path, root string }{{escape, bound}, {nested, outside}} {
		result := d.runRemoteReview(t.Context(), protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws", ProjectResourceID: "resource", DirectoryRoot: test.root, Path: test.path, Scope: "checkout", Action: "git_manifest", GitScope: "branch", Target: "main"})
		if result.Error == "" || len(result.Page) != 0 {
			t.Fatal("escaped authorized repository", result)
		}
	}
}

func TestCheckoutProjectCommitPagesAndBranches(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	repo := createWorktreeTestRepo(t)
	head := worktreeTestGit(t, repo, "rev-parse", "HEAD")
	q := protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws", ProjectResourceID: "resource", DirectoryRoot: repo, Path: repo, Scope: "checkout", Action: "git_manifest", GitScope: "commit", CommitSHA: head}
	result := d.runRemoteReview(t.Context(), q)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var manifest pagedReviewManifest
	if err := json.Unmarshal(result.Page, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Header.Head != head || !manifest.Header.CommitOnly {
		t.Fatal("selected commit lost", manifest)
	}
	q.Action, q.VersionID = "commits", manifest.VersionID
	result = d.runRemoteReview(t.Context(), q)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var page struct {
		Commits []struct {
			SHA string `json:"sha"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(result.Page, &page); err != nil || len(page.Commits) != 1 || page.Commits[0].SHA != head {
		t.Fatal("wrong commit page", string(result.Page), err)
	}
	q.Action, q.VersionID = "git_history", ""
	result = d.runRemoteReview(t.Context(), q)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if err := json.Unmarshal(result.Page, &page); err != nil || len(page.Commits) != 1 || page.Commits[0].SHA != head {
		t.Fatal("checkout history lost root", string(result.Page), err)
	}
	q.Action, q.VersionID = "branches", ""
	result = d.runRemoteReview(t.Context(), q)
	if result.Error != "" || len(result.Branches) != 1 || result.Branches[0] != "refs/heads/main" {
		t.Fatal("wrong checkout refs", result)
	}
}

func TestCheckoutVerifiedExternalDirectoryDoesNotCreateTaskRecord(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemon/runtimes/runtime/tasks/task/review-binding" || r.URL.Query().Get("directory_task_id") != "task" {
			http.Error(w, "wrong binding", http.StatusForbidden)
			return
		}
		if err := json.NewEncoder(w).Encode(protocol.LocalReviewRuntimeBinding{WorkspaceID: "ws", RuntimeID: "runtime", TaskID: "task"}); err != nil {
			t.Error(err)
		}
	}))
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	d.workspaces = map[string]*workspaceState{"ws": {runtimeIDs: []string{"runtime"}}}
	repo := createWorktreeTestRepo(t)
	result := d.runRemoteReview(t.Context(), protocol.LocalReviewCommand{WorkspaceID: "ws", RuntimeID: "runtime", TaskID: "task", Path: repo, Scope: "checkout", Action: "index"})
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if _, err := os.Stat(execenv.ReviewArchivePath(d.cfg.WorkspacesRoot, "ws", "task")); !os.IsNotExist(err) {
		t.Fatal("checkout read created per-task record", err)
	}
}
