package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorktreeInventoryRequiresCurrentReviewAndAuthoritativeLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, state, run      string
		supported, moveTarget bool
		want                  protocol.WorktreeNextAction
	}{
		{"approved", "approved", "completed", true, false, protocol.WorktreeMerge},
		{"target moved", "approved", "completed", true, true, protocol.WorktreeReview},
		{"rework", "changes_requested", "completed", true, false, protocol.WorktreeChangesRequested},
		{"active", "approved", "running", true, false, protocol.WorktreeActive},
		{"legacy server", "approved", "completed", false, false, protocol.WorktreeUnknown},
		{"unknown run", "approved", "future_status", true, false, protocol.WorktreeUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			completed := time.Now().Add(-10 * 24 * time.Hour)
			d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(protocol.TaskGCStatus{Status: tc.run, CompletedAt: completed, LifecycleSupported: tc.supported, IssueID: "issue1", IssueStatus: "in_review", IssueStatusCategory: "started"}); err != nil {
					t.Error(err)
				}
			}))
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
			repo := createWorktreeTestRepo(t)
			checkout := filepath.Join(root, "worktree")
			worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/delivery", checkout)
			worktreeTestGit(t, checkout, "commit", "--allow-empty", "-m", "delivery")
			snapshot, err := localreview.Read(t.Context(), checkout, "main")
			if err != nil {
				t.Fatal(err)
			}
			record := localreview.Record{State: tc.state, SnapshotID: snapshot.ID, SourceHead: snapshot.Head, Events: []localreview.Event{{Kind: tc.state, SnapshotID: snapshot.ID, ActorID: "owner"}}}
			if tc.state == "approved" {
				record.Events[0].Kind = "approve"
			}
			if err := localreview.SaveRecord(root, localreview.RecordKey(snapshot), record); err != nil {
				t.Fatal(err)
			}
			if tc.moveTarget {
				worktreeTestGit(t, repo, "commit", "--allow-empty", "-m", "target advanced")
			}

			r := httptest.NewRequest(http.MethodGet, "/worktrees", nil)
			r.Header.Set("Authorization", "Bearer test-token")
			r.Header.Set("X-Multica-Profile", d.cfg.Profile)
			response := httptest.NewRecorder()
			d.worktreeManagerHandler()(response, r)

			if response.Code != http.StatusOK {
				t.Fatal(response.Code, response.Body.String())
			}
			var rows []ManagedWorktree
			if err := json.Unmarshal(response.Body.Bytes(), &rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].NextAction != tc.want {
				t.Fatalf("inventory = %+v, want %s", rows, tc.want)
			}
			if rows[0].RunStatus != tc.run || rows[0].IssueID != "issue1" {
				t.Fatalf("missing authoritative identity: %+v", rows[0])
			}
			if tc.want == protocol.WorktreeMerge && (!rows[0].Stale || len(rows[0].RepositoriesDetails) != 1 || rows[0].RepositoriesDetails[0].Target != "main") {
				t.Fatalf("missing reviewed target or business age: %+v", rows[0])
			}
			if (tc.want == protocol.WorktreeActive || !tc.supported) && rows[0].Stale {
				t.Fatal("unverified or active task marked stale")
			}
		})
	}
}

func TestWorktreeInventoryPreservesPinnedDeliveryAndOutput(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		t.Run(map[bool]string{false: "output", true: "pinned delivery"}[pinned], func(t *testing.T) {
			d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(protocol.TaskGCStatus{Status: "completed", LifecycleSupported: true}); err != nil {
					t.Error(err)
				}
			}))
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
			if pinned {
				repo := createWorktreeTestRepo(t)
				if err := execenv.WriteReviewDirectory(root, execenv.ReviewDirectory{WorkspaceID: "ws1", TaskID: "task1", Path: repo, SourcePath: filepath.Join(root, "removed"), Branch: "delivery", Commit: worktreeTestGit(t, repo, "rev-parse", "HEAD")}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Join(root, "output"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "output", "report.txt"), []byte("deliverable"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			rows, err := d.managedWorktrees(t.Context())

			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].NextAction != protocol.WorktreeRetained {
				t.Fatalf("delivery reported safe to delete: %+v", rows)
			}
		})
	}
}
