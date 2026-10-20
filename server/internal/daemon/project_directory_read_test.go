package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestProjectDirectoryReadWithoutTask(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("project report"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private.txt"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, action, relative string
		reject                 bool
	}{
		{"list non Git directory", "workspace_files", "", false},
		{"read non Git file", "file_content", "report.txt", false},
		{"reject escape", "file_content", "escape/private.txt", true},
		{"reject traversal", "file_content", "../private.txt", true},
		{"reject mutation", "edit", "report.txt", true},
		{"reject non Git index", "index", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := d.runRemoteReview(t.Context(), protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws", ProjectResourceID: "resource", Path: root, Action: tc.action, RelativePath: tc.relative, Limit: 100})
			if (result.Error != "") != tc.reject {
				t.Fatalf("unexpected result: %+v", result)
			}
			if tc.reject {
				return
			}
			var page map[string]json.RawMessage
			if err := json.Unmarshal(result.Page, &page); err != nil {
				t.Fatal(err)
			}
			if tc.action == "workspace_files" {
				var entries []struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal(page["entries"], &entries); err != nil {
					t.Fatal(err)
				}
				if len(entries) != 1 || entries[0].Path != "report.txt" {
					t.Fatalf("wrong files: %+v", entries)
				}
			} else {
				var content struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(page["content"], &content); err != nil {
					t.Fatal(err)
				}
				if content.Text != "project report" {
					t.Fatalf("wrong content: %q", content.Text)
				}
			}
		})
	}
}

func TestProjectDirectoryIndexDoesNotExposeParentRepository(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(repo, "bound")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	result := d.runRemoteReview(t.Context(), protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws", ProjectResourceID: "resource", Path: path, Action: "index"})
	if result.Error == "" || len(result.Page) != 0 {
		t.Fatal("resource exposed ancestor index")
	}
}

func TestProjectDirectoryCannotUseDirectTransport(t *testing.T) {
	d := worktreeTestDaemon(t)
	data, err := json.Marshal(worktreeReviewRequest{ProjectResourceID: "resource", TaskID: "task", WorkspaceID: "ws", Path: t.TempDir(), Action: "workspace_files"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/worktrees/review", bytes.NewReader(data))
	request.Header.Set("Authorization", "Bearer "+d.client.Token())
	request.Header.Set("X-Multica-Profile", d.cfg.Profile)
	response := httptest.NewRecorder()
	d.worktreeReviewHandler()(response, request)
	if response.Code != 400 {
		t.Fatalf("resource accepted through direct IPC: %d %s", response.Code, response.Body.String())
	}
}

func TestProjectDirectoryGitIndexAndLease(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	repo := createWorktreeTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	command := protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws", ProjectResourceID: "resource", Path: repo, Action: "index"}
	result := d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var index localIndexView
	if err := json.Unmarshal(result.Page, &index); err != nil {
		t.Fatal(err)
	}
	if len(index.Status.Files) != 1 || index.Status.Files[0].Path != "new.txt" {
		t.Fatalf("wrong resource index: %+v", index)
	}
	command.Action, command.VersionID, command.Target = "lease", index.VersionID, index.Status.Branch
	result = d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	command.ProjectResourceID = "different-resource"
	result = d.runRemoteReview(t.Context(), command)
	if result.Error == "" {
		t.Fatal("cache lease escaped resource scope")
	}
}

func TestProjectDirectorySymlinkRootPreservesRequestedPath(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("bound content"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "linked-root")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private.txt"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, action, relative string
		reject                 bool
	}{
		{"list original root", "workspace_files", "", false},
		{"read bound content", "file_content", "report.txt", false},
		{"reject external content", "file_content", "escape/private.txt", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := d.runRemoteReview(t.Context(), protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws", ProjectResourceID: "resource", Path: alias, Action: tc.action, RelativePath: tc.relative, Limit: 100})
			if (result.Error != "") != tc.reject {
				t.Fatalf("unexpected result: %+v", result)
			}
			if tc.reject {
				return
			}
			var page struct {
				Path    string `json:"path"`
				Content struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(result.Page, &page); err != nil {
				t.Fatal(err)
			}
			if tc.action == "workspace_files" && page.Path != alias {
				t.Fatalf("requested path identity changed: got %q want %q", page.Path, alias)
			}
			if tc.action == "file_content" && page.Content.Text != "bound content" {
				t.Fatalf("unexpected content: %+v", page)
			}
		})
	}
}
