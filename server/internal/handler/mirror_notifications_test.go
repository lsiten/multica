package handler

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMirrorNotificationPreferenceCoversControlLifecycle(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, disconnect := range []bool{false, true} {
			t.Run(fmt.Sprintf("enabled=%t/disconnect=%t", enabled, disconnect), func(t *testing.T) {
				h := isolatedMirrorViewerHandler(t)
				workspaceID := dbfx.Workspace(t, "Mirror notifications", uuid.NewString(), testutil.Cols{
					"settings": fmt.Sprintf(`{"mirror_network":{"mode":"builtin","viewer_notifications_enabled":%t}}`, enabled),
				})
				runtimeID := dbfx.Runtime(t, "Mirror notifications", testutil.Cols{"workspace_id": workspaceID, "daemon_id": "notification-daemon"})
				dbfx.Cleanup(t, `DELETE FROM inbox_item WHERE workspace_id=$1`, workspaceID)
				identity := daemonws.ClientIdentity{DaemonID: "notification-daemon", WorkspaceID: workspaceID, RuntimeIDs: []string{runtimeID}}
				payload := protocol.MirrorControlStatePayload{WorkspaceID: workspaceID, RuntimeID: runtimeID, DaemonID: identity.DaemonID, UserID: testUserID, ViewerID: "viewer", Active: true, Source: protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "display"}}
				if err := h.HandleDaemonMirrorControlState(context.Background(), identity, payload); err != nil {
					t.Fatal(err)
				}
				if got := h.MirrorControlStates.Snapshot(runtimeID); len(got) != 1 {
					t.Fatal("notification setting suppressed active control state")
				}
				if disconnect {
					h.HandleDaemonMirrorDisconnect(context.Background(), identity)
				} else {
					payload.Active = false
					if err := h.HandleDaemonMirrorControlState(context.Background(), identity, payload); err != nil {
						t.Fatal(err)
					}
				}
				if got := h.MirrorControlStates.Snapshot(runtimeID); len(got) != 0 {
					t.Fatal("control state not cleared")
				}
				want := 0
				if enabled {
					want = 2
				}
				if notices := listMirrorControlNotices(t, runtimeID); len(notices) != want {
					t.Fatalf("notifications=%v, want %d", notices, want)
				}
			})
		}
	}
}

func TestMirrorNotificationOptOutPreservesHistoryAndSkipsNewItems(t *testing.T) {
	h := isolatedMirrorViewerHandler(t)
	workspaceID := dbfx.Workspace(t, "Mirror opt-out", uuid.NewString())
	runtimeID := dbfx.Runtime(t, "Mirror opt-out", testutil.Cols{"workspace_id": workspaceID, "daemon_id": "opt-out-daemon"})
	dbfx.Cleanup(t, `DELETE FROM inbox_item WHERE workspace_id=$1`, workspaceID)
	identity := daemonws.ClientIdentity{DaemonID: "opt-out-daemon", WorkspaceID: workspaceID, RuntimeIDs: []string{runtimeID}}
	payload := protocol.MirrorControlStatePayload{WorkspaceID: workspaceID, RuntimeID: runtimeID, DaemonID: identity.DaemonID, UserID: testUserID, ViewerID: "viewer", Active: true, Source: protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "display"}}
	if err := h.HandleDaemonMirrorControlState(context.Background(), identity, payload); err != nil {
		t.Fatal(err)
	}
	if notices := listMirrorControlNotices(t, runtimeID); len(notices) != 1 {
		t.Fatal("default setting did not notify")
	}
	dbfx.Exec(t, `UPDATE workspace SET settings='{"mirror_network":{"mode":"builtin","viewer_notifications_enabled":false}}'::jsonb WHERE id=$1`, workspaceID)
	h.HandleDaemonMirrorDisconnect(context.Background(), identity)
	if notices := listMirrorControlNotices(t, runtimeID); len(notices) != 1 || notices[0] != protocol.InboxTypeRuntimeControlStarted {
		t.Fatalf("opt-out should retain only the previous notification: %v", notices)
	}
}

func TestMirrorNotificationPreferenceStillCoversViewerLifecycle(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			h := isolatedMirrorViewerHandler(t)
			workspaceID := dbfx.Workspace(t, "Viewer notifications", uuid.NewString(), testutil.Cols{
				"settings": fmt.Sprintf(`{"mirror_network":{"mode":"builtin","viewer_notifications_enabled":%t}}`, enabled),
			})
			runtimeID := dbfx.Runtime(t, "Viewer notifications", testutil.Cols{"workspace_id": workspaceID})
			dbfx.Cleanup(t, `DELETE FROM inbox_item WHERE workspace_id=$1`, workspaceID)
			rt, err := h.Queries.GetAgentRuntime(context.Background(), parseUUID(runtimeID))
			if err != nil {
				t.Fatal(err)
			}
			for _, active := range []bool{true, false} {
				if err := h.notifyMirrorViewer(context.Background(), rt, active); err != nil {
					t.Fatal(err)
				}
			}
			want := 0
			if enabled {
				want = 2
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM inbox_item WHERE workspace_id=$1`, workspaceID); got != want {
				t.Fatalf("viewer notifications=%d, want %d", got, want)
			}
		})
	}
}
