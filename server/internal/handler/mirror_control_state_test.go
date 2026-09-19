package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func listMirrorControlNotices(t *testing.T, runtimeID string) []string {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT type
		FROM inbox_item
		WHERE details->>'runtime_id' = $1
		  AND type IN ($2, $3)
		ORDER BY created_at, id
	`, runtimeID, protocol.InboxTypeRuntimeControlStarted, protocol.InboxTypeRuntimeControlStopped)
	if err != nil {
		t.Fatalf("list mirror control notices: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var typ string
		if err := rows.Scan(&typ); err != nil {
			t.Fatalf("scan mirror control notice: %v", err)
		}
		got = append(got, typ)
	}
	return got
}

func TestHandleDaemonMirrorDisconnectClearsControlState(t *testing.T) {
	h := isolatedMirrorViewerHandler(t)
	const daemonID = "mirror-control-disconnect-daemon"
	runtimeID := dbfx.Runtime(t, "Mirror Control Disconnect Runtime", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"owner_id":     testUserID,
		"daemon_id":    daemonID,
		"custom_name":  "Controlled Box",
	})
	dbfx.Cleanup(t, `DELETE FROM inbox_item WHERE details->>'runtime_id' = $1`, runtimeID)

	controlEvents := make(chan protocol.RuntimeMirrorControlPayload, 4)
	h.Bus.Subscribe(protocol.EventRuntimeMirrorControl, func(event events.Event) {
		if payload, ok := event.Payload.(protocol.RuntimeMirrorControlPayload); ok {
			controlEvents <- payload
		}
	})

	identity := daemonws.ClientIdentity{
		DaemonID:     daemonID,
		WorkspaceID:  testWorkspaceID,
		WorkspaceIDs: []string{testWorkspaceID},
		RuntimeIDs:   []string{runtimeID},
	}
	payload := protocol.MirrorControlStatePayload{
		WorkspaceID: testWorkspaceID,
		RuntimeID:   runtimeID,
		DaemonID:    daemonID,
		ViewerID:    "viewer-1",
		UserID:      testUserID,
		Source:      protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "display-1"},
	}
	if err := h.HandleDaemonMirrorControlState(context.Background(), identity, payload); err != nil {
		t.Fatalf("start mirror control: %v", err)
	}
	if got := <-controlEvents; len(got.Controllers) != 1 {
		t.Fatalf("active control event = %+v, want one controller", got)
	}

	h.HandleDaemonMirrorDisconnect(context.Background(), identity)

	if got := h.MirrorControlStates.Snapshot(runtimeID); len(got) != 0 {
		t.Fatalf("controllers after disconnect = %+v, want empty", got)
	}
	if got := <-controlEvents; len(got.Controllers) != 0 {
		t.Fatalf("disconnect control event = %+v, want no controllers", got)
	}
	notices := listMirrorControlNotices(t, runtimeID)
	if len(notices) != 2 || notices[0] != protocol.InboxTypeRuntimeControlStarted || notices[1] != protocol.InboxTypeRuntimeControlStopped {
		t.Fatalf("control notices after disconnect = %v, want started then stopped", notices)
	}
}

func TestHandleDaemonMirrorDisconnectKeepsControlStateServedByAnotherConnection(t *testing.T) {
	h := isolatedMirrorViewerHandler(t)
	const daemonID = "mirror-control-retained-daemon"
	runtimeID := dbfx.Runtime(t, "Mirror Control Retained Runtime", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"owner_id":     testUserID,
		"daemon_id":    daemonID,
	})
	dbfx.Cleanup(t, `DELETE FROM inbox_item WHERE details->>'runtime_id' = $1`, runtimeID)

	identity := daemonws.ClientIdentity{
		DaemonID:     daemonID,
		WorkspaceID:  testWorkspaceID,
		WorkspaceIDs: []string{testWorkspaceID},
		RuntimeIDs:   []string{runtimeID},
	}
	conn := connectMirrorViewerDaemon(t, h, identity)
	defer func() { _ = conn.Close() }()

	payload := protocol.MirrorControlStatePayload{
		WorkspaceID: testWorkspaceID,
		RuntimeID:   runtimeID,
		DaemonID:    daemonID,
		ViewerID:    "viewer-1",
		UserID:      testUserID,
		Source:      protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "display-1"},
	}
	if err := h.HandleDaemonMirrorControlState(context.Background(), identity, payload); err != nil {
		t.Fatalf("start mirror control: %v", err)
	}

	h.HandleDaemonMirrorDisconnect(context.Background(), identity)

	if got := h.MirrorControlStates.Snapshot(runtimeID); len(got) != 1 {
		t.Fatalf("controllers after disconnect with a live connection = %+v, want one retained", got)
	}
	if notices := listMirrorControlNotices(t, runtimeID); len(notices) != 1 || notices[0] != protocol.InboxTypeRuntimeControlStarted {
		t.Fatalf("control notices after retained disconnect = %v, want only started", notices)
	}
}

func TestGetMirrorControlStateReturnsSnapshot(t *testing.T) {
	h := isolatedMirrorViewerHandler(t)
	runtimeID := dbfx.Runtime(t, "Mirror Control Snapshot Runtime", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"owner_id":     testUserID,
		"daemon_id":    "mirror-control-snapshot-daemon",
	})
	h.MirrorControlStates.SetState(runtimeID, mirror.ControllerState{
		ViewerID: "viewer-1",
		UserID:   testUserID,
		Source:   protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "display-1"},
	}, true)

	req := withURLParam(newRequest(http.MethodGet, "/api/runtimes/"+runtimeID+"/mirror/control-state", nil), "runtimeId", runtimeID)
	var got protocol.RuntimeMirrorControlPayload
	testutil.Call(t, h.GetMirrorControlState, req).Want(http.StatusOK).JSON(&got)
	if got.RuntimeID != runtimeID || got.WorkspaceID != testWorkspaceID || len(got.Controllers) != 1 {
		t.Fatalf("control snapshot = %+v, want one current controller", got)
	}
	if controller := got.Controllers[0]; controller.ViewerID != "viewer-1" || controller.Source.SourceID != "display-1" {
		t.Fatalf("controller = %+v, want viewer-1/display-1", controller)
	}
}
