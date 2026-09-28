package daemon

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMergedInventoryAndCleanupPreserveNewIgnoredOutput(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(protocol.TaskGCStatus{Status: "completed", LifecycleSupported: true}); err != nil {
			t.Error(err)
		}
	}))
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	checkout := filepath.Join(root, "worktree")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/delivery", checkout)
	writeLifecycleFile(t, filepath.Join(checkout, ".gitignore"), "report.txt\n")
	worktreeTestGit(t, checkout, "add", ".gitignore")
	worktreeTestGit(t, checkout, "commit", "-m", "delivery")
	snapshot, err := localreview.Read(t.Context(), checkout, "main")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := localreview.Merge(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := localreview.SaveRecord(root, localreview.RecordKey(snapshot), localreview.Record{State: "merged", SnapshotID: snapshot.ID, SourceHead: snapshot.Head, Snapshot: &snapshot, MergedCommit: commit}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(checkout, "report.txt")
	writeLifecycleFile(t, output, "")

	rows, err := d.managedWorktrees(t.Context())
	reason := d.cleanupManagedWorktree(t.Context(), worktreeCleanup{path: root})

	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].NextAction != protocol.WorktreeRetained || rows[0].ProtectionReason != "output" {
		t.Fatalf("unsafe batch candidate: %+v", rows)
	}
	if reason != "output" {
		t.Fatalf("manual cleanup without discard bypassed output protection: %s", reason)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("ignored output removed: %v", err)
	}
}

func TestLegacyFullCleanupReclaimsArtifactsAfterPreservingDelivery(t *testing.T) {
	for _, status := range []string{"completed", "future_status", "forbidden"} {
		t.Run(status, func(t *testing.T) {
			d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status == "forbidden" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if err := json.NewEncoder(w).Encode(protocol.TaskGCStatus{Status: status}); err != nil {
					t.Error(err)
				}
			}))
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", &execenv.GCMeta{CompletedAt: time.Now().Add(-48 * time.Hour)})
			repo := createWorktreeTestRepo(t)
			checkout := filepath.Join(root, "worktree")
			worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/artifacts", checkout)
			writeLifecycleFile(t, filepath.Join(checkout, ".gitignore"), "node_modules/\n")
			artifact := filepath.Join(checkout, "node_modules", "cache")
			writeLifecycleFile(t, artifact, "cache")
			output := filepath.Join(root, "output", "report.txt")
			writeLifecycleFile(t, output, "unique delivery")

			d.applyGCAction(root, gcActionClean, &gcStats{byPattern: map[string]int{}})

			if _, err := os.Stat(output); err != nil {
				t.Fatalf("delivery removed: %v", err)
			}
			_, err := os.Stat(artifact)
			if status == "completed" && !os.IsNotExist(err) {
				t.Fatalf("safe artifact not reclaimed: %v", err)
			}
			if status != "completed" && err != nil {
				t.Fatalf("unknown status authorized artifact removal: %v", err)
			}
		})
	}
}
