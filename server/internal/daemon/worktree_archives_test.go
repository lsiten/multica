package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestWorktreeArchivePreservesOutputsAndRestoresWithoutDiscard(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", &execenv.GCMeta{WorkspaceID: "ws1", TaskID: "task1", LocalDirectory: true, ProjectID: "project", ProjectName: "Project"})
	repo := createWorktreeTestRepo(t)
	checkout := filepath.Join(root, "worktree")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/archive", checkout)
	writeLifecycleFile(t, filepath.Join(checkout, "unfinished.txt"), "uncommitted source")
	writeLifecycleFile(t, filepath.Join(root, "output", "result.txt"), "result")
	writeLifecycleFile(t, filepath.Join(root, "logs", "run.log"), "log")
	writeLifecycleFile(t, filepath.Join(root, "codex-home", ".sandbox-bin", "binary"), "cache")
	preview := d.archiveEnvironmentOperation(t.Context(), root, "", "")
	if preview.Reason != "" || preview.Revision == "" {
		t.Fatalf("preview: %+v", preview)
	}
	result := d.archiveEnvironmentOperation(t.Context(), root, preview.Revision, strings.Repeat("a", 64))
	if result.Reason != "" || !result.Reclaimed || result.ArchiveBytes <= 0 {
		t.Fatalf("archive: %+v", result)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("environment remains: %v", err)
	}
	if list := worktreeTestGit(t, repo, "worktree", "list", "--porcelain"); strings.Contains(list, "agent/archive") {
		t.Fatalf("worktree registration remains: %s", list)
	}
	rows, err := d.listEnvironmentArchives(t.Context(), "ws1")
	if err != nil || len(rows) != 1 || rows[0].ProjectID != "project" || rows[0].RestoreReason != "" {
		t.Fatalf("archive inventory: %+v, %v", rows, err)
	}
	if restored := d.restoreEnvironmentOperation(t.Context(), result.ArchiveID, "other-workspace"); restored.Reason != "unowned" {
		t.Fatalf("cross-workspace restore: %+v", restored)
	}
	if restored := d.restoreEnvironmentOperation(t.Context(), result.ArchiveID, "ws1"); !restored.Restored || restored.Reason != "" {
		t.Fatalf("restore: %+v", restored)
	}
	for _, relative := range []string{"worktree/unfinished.txt", "output/result.txt", "logs/run.log"} {
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatalf("%s lost: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "codex-home", ".sandbox-bin")); !os.IsNotExist(err) {
		t.Fatalf("cache restored: %v", err)
	}
	if restored := d.restoreEnvironmentOperation(t.Context(), result.ArchiveID, "ws1"); restored.Reason != "existing_environment" {
		t.Fatalf("existing environment overwritten: %+v", restored)
	}
}

func TestWorktreeArchiveRejectsActivityStalePreviewsAndUnknownRoots(t *testing.T) {
	for _, condition := range []string{"active", "changed", "unowned"} {
		t.Run(condition, func(t *testing.T) {
			d := worktreeTestDaemon(t)
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
			writeLifecycleFile(t, filepath.Join(root, "output", "result"), "keep")
			preview := d.archiveEnvironmentOperation(t.Context(), root, "", "")
			selected := root
			switch condition {
			case "active":
				d.markActiveEnvRoot(root)
			case "changed":
				writeLifecycleFile(t, filepath.Join(root, "output", "result"), "new work")
			case "unowned":
				selected = t.TempDir()
			}
			result := d.archiveEnvironmentOperation(t.Context(), selected, preview.Revision, strings.Repeat("a", 64))
			if result.Reason == "" || result.Reclaimed {
				t.Fatalf("unsafe archive: %+v", result)
			}
			if _, err := os.Stat(filepath.Join(root, "output", "result")); err != nil {
				t.Fatalf("source lost: %v", err)
			}
		})
	}
}

func TestWorktreeArchiveAPIRequiresIdentityAndOwnerAuthentication(t *testing.T) {
	d := worktreeTestDaemon(t)
	createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	for _, tc := range []struct {
		body, token string
		status      int
	}{
		{`{"action":"preview"}`, "", http.StatusUnauthorized},
		{`{"action":"archive"}`, "test-token", http.StatusBadRequest},
		{`{"action":"restore","archive_id":"/"}`, "test-token", http.StatusBadRequest},
		{`{"action":"preview","paths":["/"]}`, "test-token", http.StatusBadRequest},
		{`{"action":"preview","workspace_id":"other"}`, "test-token", http.StatusOK},
		{`{"action":"preview","workspace_id":"ws1"}`, "test-token", http.StatusOK},
	} {
		request := httptest.NewRequest(http.MethodPost, "/worktrees/archives", strings.NewReader(tc.body))
		request.Header.Set("Authorization", "Bearer "+tc.token)
		response := httptest.NewRecorder()
		d.worktreeManagerHandler()(response, request)
		if response.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.body, response.Code, response.Body.String())
		}
		if tc.status == http.StatusOK {
			var rows []worktreeArchiveResult
			if err := json.Unmarshal(response.Body.Bytes(), &rows); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(tc.body, "other") && len(rows) != 0 {
				t.Fatalf("cross-workspace preview: %+v", rows)
			}
		}
	}
}

func TestWorktreeArchiveNeverReclaimsWhenBackendStatusUnavailable(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "offline", http.StatusServiceUnavailable) }))
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	writeLifecycleFile(t, filepath.Join(root, "output", "result"), "keep")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result := d.archiveEnvironmentOperation(ctx, root, strings.Repeat("a", 64), strings.Repeat("b", 64))
	if result.Reason != "unavailable" || result.Reclaimed {
		t.Fatalf("backend failure authorized archive: %+v", result)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal(err)
	}
}
