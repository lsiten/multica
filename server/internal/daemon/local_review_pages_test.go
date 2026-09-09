package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRemoteReviewManifestAndFilePagesStayPinned(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	for i := 0; i < 201; i++ {
		if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("%03d.txt", i)), []byte("small\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(path, "large.txt"), []byte(strings.Repeat("captured large line\n", 500000)), 0600); err != nil {
		t.Fatal(err)
	}
	command := protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws1", TaskID: "task1", Path: path, Target: "main", Action: "manifest", Limit: 100}
	result := d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var manifest pagedReviewManifest
	if err := json.Unmarshal(result.Page, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.VersionID) != 64 || manifest.Page.TotalFiles != 202 || len(manifest.Page.Files) != 100 || !manifest.Page.HasMore {
		t.Fatalf("bad manifest: %+v", manifest)
	}
	if len(result.Page) > 256<<10 || len(result.Snapshot) != 0 {
		t.Fatal("manifest eagerly returned patch payload")
	}
	leaseCommand := command
	leaseCommand.Action, leaseCommand.VersionID = "lease", manifest.VersionID
	leaseResult := d.runRemoteReview(t.Context(), leaseCommand)
	if leaseResult.Error != "" {
		t.Fatal(leaseResult.Error)
	}
	var lease struct {
		VersionID string `json:"version_id"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(leaseResult.Page, &lease); err != nil || lease.VersionID != manifest.VersionID || lease.ExpiresAt == "" {
		t.Fatalf("lease identity missing: %+v %v", lease, err)
	}
	command.Action, command.VersionID, command.Offset = "files", manifest.VersionID, 100
	result = d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if err := json.Unmarshal(result.Page, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Page.NextOffset != 200 || len(manifest.Page.Files) != 100 {
		t.Fatalf("bad second page: %+v", manifest.Page)
	}
	if err := os.WriteFile(filepath.Join(path, "large.txt"), []byte("later checkout content"), 0600); err != nil {
		t.Fatal(err)
	}
	command.Action, command.FilePath, command.Offset, command.Limit = "file", "large.txt", 0, 30
	result = d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var file pagedReviewFile
	if err := json.Unmarshal(result.Page, &file); err != nil {
		t.Fatal(err)
	}
	if file.Page == nil || !file.Page.HasMore || len(file.Page.Lines) != 30 {
		t.Fatalf("large file was not paged: %+v", file)
	}
	for _, line := range file.Page.Lines {
		if strings.Contains(line.Text, "later checkout") {
			t.Fatal("version mixed live contents")
		}
	}
	command.FilePath = "../outside"
	result = d.runRemoteReview(t.Context(), command)
	if result.Error == "" || len(result.Page) != 0 {
		t.Fatal("file request bypassed manifest membership")
	}
	command.Action, command.FilePath, command.Side, command.Offset, command.Limit = "content", "large.txt", "new", 0, 5
	result = d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var content struct {
		Content struct {
			Text       string `json:"text"`
			NextOffset int64  `json:"next_offset"`
		} `json:"content"`
	}
	if err := json.Unmarshal(result.Page, &content); err != nil {
		t.Fatal(err)
	}
	if content.Content.Text != "captu" || content.Content.NextOffset != 5 {
		t.Fatalf("content view read live data: %+v", content)
	}
}

func TestRemoteReviewFileLimitsDoNotBlockManifestOrOtherFiles(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	for name, contents := range map[string]string{"binary.bin": "\x00binary", "minified.txt": strings.Repeat("x", 128<<10), "small.txt": "small change\n"} {
		if err := os.WriteFile(filepath.Join(path, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws1", TaskID: "task1", Path: path, Target: "main", Action: "manifest"}
	result := d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var manifest pagedReviewManifest
	if err := json.Unmarshal(result.Page, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Page.TotalFiles != 3 {
		t.Fatalf("some files disappeared: %+v", manifest.Page)
	}
	command.Action, command.VersionID = "file", manifest.VersionID
	for name, want := range map[string]string{"binary.bin": "binary", "minified.txt": "too_large", "small.txt": "text"} {
		command.FilePath = name
		result := d.runRemoteReview(t.Context(), command)
		if result.Error != "" {
			t.Fatal(result.Error)
		}
		var file pagedReviewFile
		if err := json.Unmarshal(result.Page, &file); err != nil {
			t.Fatal(err)
		}
		if file.Preview != want {
			t.Fatalf("%s: preview=%s want %s", name, file.Preview, want)
		}
		if want == "text" && file.Page == nil {
			t.Fatal("small file lost its diff")
		}
	}
	command.TaskID = "another-task"
	if result := d.runRemoteReview(t.Context(), command); result.Error == "" {
		t.Fatal("paged review bypassed task ownership")
	}
}

func TestPagedReviewDiscoversMultipleRepositoriesWithoutTarget(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	parent := filepath.Join(root, "workdir")
	first, second := filepath.Join(parent, "alpha"), filepath.Join(parent, "beta")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "alpha", first)
	worktreeTestGit(t, repo, "worktree", "add", "-b", "beta", second)
	result := d.runRemoteReview(t.Context(), protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws1", TaskID: "task1", Path: parent, Action: "repositories"})
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var page struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.Unmarshal(result.Page, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Repositories) != 2 || page.Repositories[0] != first || page.Repositories[1] != second {
		t.Fatalf("discovery lost logical task paths: %+v", page)
	}
}
