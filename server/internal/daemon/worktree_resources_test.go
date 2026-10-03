package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestWorktreeResourceAPIRequiresPreviewAndWorkspaceOwnership(t *testing.T) {
	d := worktreeTestDaemon(t)
	first := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	second := createTaskDir(t, d.cfg.WorkspacesRoot, "ws2", "task2", nil)
	for _, root := range []string{first, second} {
		writeLifecycleFile(t, filepath.Join(root, execenv.ManagedReclaimableArtifactSubpaths()[0], "binary"), "cache")
	}
	call := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/worktrees", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer test-token")
		response := httptest.NewRecorder()
		d.worktreeManagerHandler()(response, request)
		return response
	}
	preview := call(`{"action":"preview_cache","workspace_id":"ws1"}`)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	var results []worktreeCacheResult
	if err := json.Unmarshal(preview.Body.Bytes(), &results); err != nil || len(results) != 1 || results[0].WorkspaceID != "ws1" {
		t.Fatalf("workspace filter failed: %+v %v", results, err)
	}
	selection := worktreeCacheSelection{EnvironmentID: results[0].EnvironmentID, Revision: results[0].Revision}
	for _, workspace := range []string{"ws2", "ws1"} {
		body, err := json.Marshal(worktreeResourceRequest{Action: "clean_cache", WorkspaceID: workspace, Selections: []worktreeCacheSelection{selection, selection}})
		if err != nil {
			t.Fatal(err)
		}
		response := call(string(body))
		if response.Code != http.StatusOK {
			t.Fatalf("cleanup: %d %s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &results); err != nil || len(results) != 1 {
			t.Fatalf("duplicate selection: %+v %v", results, err)
		}
		if workspace == "ws2" && (results[0].Reason != "unowned" || results[0].RemovedCount != 0) {
			t.Fatalf("cross-workspace cleanup accepted: %+v", results)
		}
		if workspace == "ws1" && results[0].RemovedCount != 1 {
			t.Fatalf("cache cleanup failed: %+v", results)
		}
	}
	for _, body := range []string{`{"action":"clean_cache"}`, `{"action":"clean_cache","selections":[{"environment_id":"path","revision":""}]}`, `{"action":"preview_cache","discard_changes":true}`} {
		if response := call(body); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid operation accepted: %s (%d)", body, response.Code)
		}
	}
}

func TestWorktreeCachePreviewAndCleanupPreserveEnvironment(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", &execenv.GCMeta{LocalDirectory: true})
	cache := filepath.Join(root, execenv.ManagedReclaimableArtifactSubpaths()[0], "codex")
	writeLifecycleFile(t, cache, "regenerable binary")
	for _, path := range []string{"output/result.md", "logbook/run.log", "codex-home/config.toml", "codex-home/session.json"} {
		writeLifecycleFile(t, filepath.Join(root, path), "preserve")
	}
	preview := d.worktreeCacheOperation(t.Context(), root, "")
	if preview.Reason != "" || preview.SizeBytes != int64(len("regenerable binary")) || preview.Revision == "" || len(preview.Candidates) != 1 {
		t.Fatalf("invalid preview: %+v", preview)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("preview mutated cache: %v", err)
	}
	result := d.worktreeCacheOperation(t.Context(), root, preview.Revision)
	if result.Reason != "" || result.RemovedCount != 1 || result.RemovedBytes != preview.SizeBytes {
		t.Fatalf("invalid receipt: %+v", result)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("cache remains: %v", err)
	}
	for _, path := range []string{"output/result.md", "logbook/run.log", "codex-home/config.toml", "codex-home/session.json"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("protected resource %s lost: %v", path, err)
		}
	}
}

func TestWorktreeCacheExecutionRevalidatesPreviewAndActivity(t *testing.T) {
	for _, changed := range []string{"cache", "active", "unowned"} {
		t.Run(changed, func(t *testing.T) {
			d := worktreeTestDaemon(t)
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
			cache := filepath.Join(root, execenv.ManagedReclaimableArtifactSubpaths()[0], "codex")
			writeLifecycleFile(t, cache, "binary")
			preview := d.worktreeCacheOperation(t.Context(), root, "")
			switch changed {
			case "cache":
				writeLifecycleFile(t, cache, "changed binary")
			case "active":
				d.markActiveEnvRoot(root)
			case "unowned":
				root = t.TempDir()
			}
			result := d.worktreeCacheOperation(t.Context(), root, preview.Revision)
			if result.Reason == "" || result.RemovedCount != 0 {
				t.Fatalf("changed preview executed: %+v", result)
			}
			if _, err := os.Stat(cache); err != nil {
				t.Fatalf("protected cache lost: %v", err)
			}
		})
	}
}

func TestWorktreeCachePreservesSourceAndExternalDirectories(t *testing.T) {
	for _, kind := range []string{"tracked", "untracked", "nested", "symlink", "internal_link", "ignored"} {
		t.Run(kind, func(t *testing.T) {
			d := worktreeTestDaemon(t)
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
			repo := createWorktreeTestRepo(t)
			checkout := filepath.Join(root, "worktree")
			worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/cache", checkout)
			if kind != "untracked" {
				writeLifecycleFile(t, filepath.Join(checkout, ".gitignore"), "node_modules/\n")
				worktreeTestGit(t, checkout, "add", ".gitignore")
				worktreeTestGit(t, checkout, "commit", "-m", "ignore dependencies")
			}
			cache := filepath.Join(checkout, "node_modules")
			writeLifecycleFile(t, filepath.Join(cache, "package.js"), "source")
			switch kind {
			case "tracked":
				worktreeTestGit(t, checkout, "add", "-f", "node_modules/package.js")
			case "nested":
				worktreeTestGit(t, repo, "clone", repo, filepath.Join(cache, "nested"))
			case "symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(cache, "external")); err != nil {
					t.Fatal(err)
				}
			case "internal_link":
				if err := os.Symlink("package.js", filepath.Join(cache, "cli")); err != nil {
					t.Fatal(err)
				}
			}
			preview := d.worktreeCacheOperation(t.Context(), root, "")
			if preview.Reason != "" {
				t.Fatalf("preview: %+v", preview)
			}
			if kind == "ignored" || kind == "internal_link" {
				if len(preview.Candidates) != 1 {
					t.Fatalf("ignored dependency cache not found: %+v", preview)
				}
				d.worktreeCacheOperation(t.Context(), root, preview.Revision)
				if _, err := os.Stat(cache); !os.IsNotExist(err) {
					t.Fatalf("ignored cache remains: %v", err)
				}
			} else if len(preview.Candidates) != 0 {
				t.Fatalf("%s falsely reclaimable: %+v", kind, preview)
			}
		})
	}
}
