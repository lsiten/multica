package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestLocalReviewRetiredWritesPreserveRepositoryAndReceipts(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "loopback", true: "queued"}[remote], func(t *testing.T) {
			d := worktreeTestDaemon(t)
			d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
			d.workspaces = map[string]*workspaceState{"ws": {runtimeIDs: []string{"runtime"}}}
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws", "task", nil)
			repo := createWorktreeTestRepo(t)
			path := filepath.Join(root, "workdir", "repo")
			worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
			if err := os.WriteFile(filepath.Join(path, "pending.txt"), []byte("staged\n"), 0600); err != nil {
				t.Fatal(err)
			}
			worktreeTestGit(t, path, "add", "pending.txt")
			if err := os.WriteFile(filepath.Join(path, "pending.txt"), []byte("working edit\n"), 0600); err != nil {
				t.Fatal(err)
			}
			snapshot, err := localreview.Read(t.Context(), path, "main")
			if err != nil {
				t.Fatal(err)
			}
			key := localreview.RecordKey(snapshot)
			record := localreview.Record{State: "approved", SnapshotID: snapshot.ID, SourceHead: snapshot.Head, PreparedCommit: snapshot.Head, Snapshot: &snapshot, CommandID: "legacy-retry", Events: []localreview.Event{{Kind: "approve", SnapshotID: snapshot.ID}}}
			if err := localreview.SaveRecord(root, key, record); err != nil {
				t.Fatal(err)
			}
			recordPath := filepath.Join(root, ".local-review-"+key+".json")
			receipt, err := os.ReadFile(recordPath)
			if err != nil {
				t.Fatal(err)
			}
			indexPath := worktreeTestGit(t, path, "rev-parse", "--git-path", "index")
			index, err := os.ReadFile(indexPath)
			if err != nil {
				t.Fatal(err)
			}
			source, target := worktreeTestGit(t, path, "rev-parse", "HEAD"), worktreeTestGit(t, repo, "rev-parse", "HEAD")
			for _, action := range []string{"submit", "approve", "request_changes", "merge", "merge_selected", "stage", "unstage", "commit"} {
				if remote {
					result, report := d.runClaimedReview(t.Context(), protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws", TaskID: "task", Path: path, Target: "main", Action: action, CommandID: "legacy-retry", SnapshotID: snapshot.ID, Paths: []string{"pending.txt"}, Message: "legacy", ClaimToken: "claim"})
					if !report || result.Error != protocol.LocalReviewWritesRetired || result.ClaimToken != "claim" || result.MergedCommit != "" {
						t.Fatalf("%s not terminal: %+v report=%v", action, result, report)
					}
				} else {
					body, err := json.Marshal(worktreeReviewRequest{RuntimeID: "runtime", WorkspaceID: "ws", TaskID: "task", Path: path, Target: "main", Action: action, CommandID: "legacy-retry", SnapshotID: snapshot.ID, Paths: []string{"pending.txt"}, Message: "legacy"})
					if err != nil {
						t.Fatal(err)
					}
					r := httptest.NewRequest(http.MethodPost, "/worktrees/review", bytes.NewReader(body))
					r.Header.Set("Authorization", "Bearer test-token")
					r.Header.Set("X-Multica-Profile", d.cfg.Profile)
					w := httptest.NewRecorder()
					d.worktreeReviewHandler()(w, r)
					if w.Code != http.StatusGone || strings.TrimSpace(w.Body.String()) != protocol.LocalReviewWritesRetired {
						t.Fatal(action, w.Code, w.Body.String())
					}
				}
			}
			afterIndex, err := os.ReadFile(indexPath)
			if err != nil {
				t.Fatal(err)
			}
			afterReceipt, err := os.ReadFile(recordPath)
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(path, "pending.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(index, afterIndex) || !bytes.Equal(receipt, afterReceipt) || string(content) != "working edit\n" || worktreeTestGit(t, path, "rev-parse", "HEAD") != source || worktreeTestGit(t, repo, "rev-parse", "HEAD") != target {
				t.Fatal("retired write mutated repository or persisted history")
			}
		})
	}
}
