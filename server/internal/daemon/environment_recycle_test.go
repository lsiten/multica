package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAutomaticEnvironmentRecyclingFollowsCompletedRunAndParentEvents(t *testing.T) {
	for _, kind := range []string{"issue", "done_issue", "review_issue", "recent_issue", "archived_chat", "active_chat", "completed_automation", "automation_review_issue", "running_automation", "recent_automation", "running_task", "retention", "foreign_workspace", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(r.URL.Path, "chat-sessions"):
					state := "archived"
					if kind == "active_chat" {
						state = "active"
					}
					json.NewEncoder(w).Encode(map[string]any{"status": state, "updated_at": time.Now().Add(-48 * time.Hour)})
				case strings.Contains(r.URL.Path, "autopilot-runs"):
					state := "completed"
					if kind == "running_automation" {
						state = "running"
					}
					completed := time.Now().Add(-48 * time.Hour)
					if kind == "recent_automation" {
						completed = time.Now()
					}
					json.NewEncoder(w).Encode(map[string]any{"status": state, "completed_at": completed})
				default:
					state := "completed"
					if kind == "running_task" {
						state = "running"
					}
					workspace := "ws1"
					if kind == "foreign_workspace" {
						workspace = "ws2"
					}
					status := protocol.TaskGCStatus{Status: state, LifecycleSupported: true, WorkspaceID: workspace, CompletedAt: time.Now().Add(-48 * time.Hour)}
					if kind == "archived_chat" || kind == "active_chat" {
						status.ChatSessionID = "chat"
					}
					if kind == "completed_automation" || kind == "running_automation" || kind == "recent_automation" || kind == "automation_review_issue" {
						status.AutopilotRunID = "run"
					}
					if kind == "automation_review_issue" {
						status.IssueID, status.IssueStatusCategory = "issue", "started"
					}
					if kind == "done_issue" || kind == "review_issue" || kind == "recent_issue" {
						status.IssueID, status.IssueStatusCategory = "issue", "done"
						if kind == "review_issue" {
							status.IssueStatusCategory = "started"
						}
						if kind == "recent_issue" {
							recent := time.Now()
							status.LastActivityAt = &recent
						}
					}
					json.NewEncoder(w).Encode(status)
				}
			}))
			d.cfg.GCEnabled, d.cfg.EnvironmentArchiveEnabled, d.cfg.EnvironmentArchiveTTL = true, true, 24*time.Hour
			meta := &execenv.GCMeta{Kind: execenv.GCKindIssue, WorkspaceID: "ws1", TaskID: "task", CompletedAt: time.Now().Add(-48 * time.Hour), LocalDirectory: true}
			switch kind {
			case "archived_chat", "active_chat":
				meta.Kind, meta.ChatSessionID = execenv.GCKindChat, "chat"
			case "completed_automation", "running_automation", "recent_automation", "automation_review_issue":
				meta.Kind, meta.AutopilotRunID = execenv.GCKindAutopilotRun, "run"
			case "retention":
				meta.CompletedAt = time.Now()
			case "unknown":
				meta.Kind = "future"
			}
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", meta)
			eligible, _ := d.automaticArchiveEligible(t.Context(), root, meta)
			want := kind == "issue" || kind == "done_issue" || kind == "archived_chat" || kind == "completed_automation"
			if eligible != want {
				t.Fatalf("%s: eligible %v, want %v", kind, eligible, want)
			}
			if want {
				d.markActiveEnvRoot(root)
				if eligible, _ := d.automaticArchiveEligible(t.Context(), root, meta); eligible {
					t.Fatal("active execution became recyclable")
				}
			}
		})
	}
}

func TestAutomaticArchiveRetainsChatReactivatedDuringCapture(t *testing.T) {
	var checks atomic.Int32
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "chat-sessions") {
			state := "archived"
			if checks.Add(1) > 1 {
				state = "active"
			}
			json.NewEncoder(w).Encode(map[string]any{"status": state, "updated_at": time.Now().Add(-48 * time.Hour)})
			return
		}
		json.NewEncoder(w).Encode(protocol.TaskGCStatus{Status: "completed", LifecycleSupported: true, ChatSessionID: "chat", WorkspaceID: "ws1", CompletedAt: time.Now().Add(-48 * time.Hour)})
	}))
	d.cfg.GCEnabled, d.cfg.EnvironmentArchiveEnabled = true, true
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", &execenv.GCMeta{Kind: execenv.GCKindChat, ChatSessionID: "chat", WorkspaceID: "ws1", TaskID: "task", CompletedAt: time.Now().Add(-48 * time.Hour)})
	writeLifecycleFile(t, filepath.Join(root, "output", "deliverable"), "retain output")
	preview := d.archiveEnvironmentOperation(t.Context(), root, "", "")
	ctx := withEnvironmentScope(t.Context(), environmentOperationScope{WorkspaceID: "ws1", Automatic: true})
	result := d.archiveEnvironmentOperation(ctx, root, preview.Revision, strings.Repeat("a", 64))
	if result.Reclaimed || result.Reason != "active_chat" {
		t.Fatalf("reactivated chat was reclaimed: %+v", result)
	}
	if data, err := os.ReadFile(filepath.Join(root, "output", "deliverable")); err != nil || string(data) != "retain output" {
		t.Fatalf("live chat output lost: %q %v", data, err)
	}
}

func TestAutomaticRecyclingReceiptUsesAuthoritativeRuntime(t *testing.T) {
	d := scopedEnvironmentTestDaemon(t)
	d.cfg.GCEnabled, d.cfg.EnvironmentArchiveEnabled = true, true
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", &execenv.GCMeta{Kind: execenv.GCKindIssue, WorkspaceID: "ws1", RuntimeID: "stale-runtime", TaskID: "task", CompletedAt: time.Now().Add(-time.Hour)})
	if !d.scheduleAutomaticEnvironmentRecycle(t.Context(), root) {
		t.Fatal("automatic archive was not scheduled")
	}
	d.environmentOperationWorkers.Wait()
	rows, err := d.listEnvironmentOperations(environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "runtime"})
	if err != nil || len(rows) != 1 || !rows[0].Automatic || rows[0].Status != "completed" || !strings.Contains(string(rows[0].Results[0]), `"reclaimed":true`) {
		t.Fatalf("runtime receipt missing: %+v %v", rows, err)
	}
	other, err := d.listEnvironmentOperations(environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "other-runtime"})
	if err != nil || len(other) != 0 {
		t.Fatalf("receipt crossed runtime: %+v %v", other, err)
	}
}

func TestAutomaticRecycleScanHandlesParentEventsAfterWorkerExited(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "chat-sessions"):
			state := "archived"
			if strings.Contains(r.URL.Path, "live") {
				state = "active"
			}
			json.NewEncoder(w).Encode(map[string]any{"status": state, "updated_at": time.Now().Add(-48 * time.Hour)})
		case strings.Contains(r.URL.Path, "autopilot-runs"):
			json.NewEncoder(w).Encode(AutopilotRunGCStatus{Status: "completed", CompletedAt: time.Now().Add(-48 * time.Hour)})
		default:
			status := protocol.TaskGCStatus{Status: "completed", LifecycleSupported: true, WorkspaceID: "ws1"}
			if strings.Contains(r.URL.Path, "/chat/") {
				status.ChatSessionID = "chat"
			}
			if strings.Contains(r.URL.Path, "/live/") {
				status.ChatSessionID = "live"
			}
			if strings.Contains(r.URL.Path, "/automation/") {
				status.AutopilotRunID = "run"
			}
			json.NewEncoder(w).Encode(status)
		}
	}))
	d.rootCtx = t.Context()
	t.Cleanup(d.stopEnvironmentOperations)
	d.cfg.GCEnabled, d.cfg.EnvironmentArchiveEnabled = true, true
	d.cfg.EnvironmentArchiveTTL = 24 * time.Hour
	chat := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "chat", &execenv.GCMeta{Kind: execenv.GCKindChat, ChatSessionID: "chat", TaskID: "chat", WorkspaceID: "ws1", CompletedAt: time.Now().Add(-48 * time.Hour)})
	live := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "live", &execenv.GCMeta{Kind: execenv.GCKindChat, ChatSessionID: "live", TaskID: "live", WorkspaceID: "ws1", CompletedAt: time.Now().Add(-48 * time.Hour)})
	automation := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "automation", &execenv.GCMeta{Kind: execenv.GCKindAutopilotRun, AutopilotRunID: "run", TaskID: "automation", WorkspaceID: "ws1", CompletedAt: time.Now().Add(-48 * time.Hour)})
	d.scanAutomaticEnvironmentRecycle(t.Context())
	d.environmentOperationWorkers.Wait()
	for _, path := range []string{chat, automation} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("finished parent environment remains: %s %v", path, err)
		}
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("active chat environment lost: %v", err)
	}
	rows, err := d.listEnvironmentArchives(t.Context(), "ws1")
	if err != nil || len(rows) != 2 {
		t.Fatalf("parent event recovery archives missing: %+v %v", rows, err)
	}
}

func TestAutomaticRecycleRetainsLegacyServerAndMismatchedMetadata(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.cfg.GCEnabled, d.cfg.EnvironmentArchiveEnabled = true, true
	meta := &execenv.GCMeta{Kind: execenv.GCKindChat, ChatSessionID: "chat", TaskID: "task", WorkspaceID: "ws1", CompletedAt: time.Now().Add(-48 * time.Hour)}
	path := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", meta)
	if eligible, reason := d.automaticArchiveEligible(t.Context(), path, meta); eligible || reason != "unavailable" {
		t.Fatalf("server without lifecycle contract allowed automatic reclamation: %v %s", eligible, reason)
	}
	meta.TaskID = "other"
	if eligible, reason := d.automaticArchiveEligible(t.Context(), path, meta); eligible || reason != "scope_changed" {
		t.Fatalf("mismatched metadata allowed automatic reclamation: %v %s", eligible, reason)
	}
}

func TestAutomaticRecyclingReservesCapacityForInteractiveOperations(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.rootCtx = t.Context()
	t.Cleanup(d.stopEnvironmentOperations)
	_, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	d.environmentOperations = map[string]*environmentOperationLive{
		"first":  {record: environmentOperationRecord{EnvironmentOperationStatus: protocol.EnvironmentOperationStatus{Automatic: true}}, cancel: cancel},
		"second": {record: environmentOperationRecord{EnvironmentOperationStatus: protocol.EnvironmentOperationStatus{Automatic: true}}, cancel: cancel},
	}
	request := protocol.EnvironmentOperationRequest{ID: strings.Repeat("a", 64), Action: "archive", Selections: []protocol.EnvironmentSelection{{EnvironmentID: strings.Repeat("b", 64), Revision: strings.Repeat("c", 64)}}}
	if d.automaticEnvironmentCapacityAvailable() {
		t.Fatal("background archival exceeded its concurrency ceiling")
	}
	if _, err := d.startEnvironmentOperation(environmentOperationScope{WorkspaceID: "ws1", Automatic: true}, request); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("automatic operation was not bounded: %v", err)
	}
	if _, err := d.startEnvironmentOperation(environmentOperationScope{WorkspaceID: "ws1"}, request); err != nil {
		t.Fatalf("interactive operation blocked by automatic quota: %v", err)
	}
	d.environmentOperationWorkers.Wait()
}

func TestAutomaticArchiveReclaimsLocalDirectoryOutputsOnlyAfterVerifiedBackup(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(protocol.TaskGCStatus{Status: "completed", LifecycleSupported: true, WorkspaceID: "ws1"})
	}))
	d.rootCtx = t.Context()
	t.Cleanup(d.stopEnvironmentOperations)
	d.cfg.GCEnabled, d.cfg.EnvironmentArchiveEnabled, d.cfg.EnvironmentArchiveTTL = true, true, 0
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", &execenv.GCMeta{Kind: execenv.GCKindIssue, WorkspaceID: "ws1", TaskID: "task", CompletedAt: time.Now().Add(-time.Hour), LocalDirectory: true})
	writeLifecycleFile(t, filepath.Join(root, "output", "deliverable"), "retain output")
	if !d.scheduleAutomaticEnvironmentRecycle(t.Context(), root) {
		t.Fatal("automatic archival not scheduled")
	}
	d.environmentOperationWorkers.Wait()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("idle environment remains: %v", err)
	}
	rows, err := d.listEnvironmentArchives(t.Context(), "ws1")
	if err != nil || len(rows) != 1 {
		t.Fatalf("backup missing: %+v %v", rows, err)
	}
	if result := d.restoreEnvironmentOperation(t.Context(), rows[0].ArchiveID, "ws1"); !result.Restored {
		t.Fatalf("automatic backup cannot restore: %+v", result)
	}
	if data, err := os.ReadFile(filepath.Join(root, "output", "deliverable")); err != nil || string(data) != "retain output" {
		t.Fatalf("output lost: %q %v", data, err)
	}
	d.cfg.EnvironmentArchiveTTL = 24 * time.Hour
	meta, err := execenv.ReadGCMeta(root)
	if err != nil {
		t.Fatal(err)
	}
	if eligible, reason := d.automaticArchiveEligible(t.Context(), root, meta); eligible || reason != "retention" {
		t.Fatalf("restored environment immediately recycled: %v %s", eligible, reason)
	}
}
