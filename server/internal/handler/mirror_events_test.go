package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func mirrorEventsRequest(t *testing.T, method string) *http.Request {
	t.Helper()
	req := newRequest(method, "/api/workspaces/"+testWorkspaceID+"/mirror/events", nil)
	return withURLParam(req, "id", testWorkspaceID)
}

type mirrorEventsWire struct {
	Events []struct {
		ID          string `json:"id"`
		RuntimeID   string `json:"runtime_id"`
		RuntimeName string `json:"runtime_name"`
		Event       string `json:"event"`
	} `json:"events"`
}

func TestListAndClearMirrorEvents(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	runtimeUUID := parseUUID("11111111-1111-4111-8111-111111111111")
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO runtime_mirror_event
			(workspace_id, runtime_id, runtime_name, event, failure_reason, viewer_id)
		VALUES ($1, $2, 'Events test runtime', 'session_failed', 'no permission', 'viewer-1')`,
		parseUUID(testWorkspaceID), runtimeUUID)
	if err != nil {
		t.Fatalf("insert mirror event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM runtime_mirror_event WHERE workspace_id = $1`, parseUUID(testWorkspaceID))
	})

	// Member-visible list.
	var listed mirrorEventsWire
	testutil.Call(t, testHandler.ListMirrorEvents,
		mirrorEventsRequest(t, http.MethodGet),
	).Want(http.StatusOK).JSON(&listed)

	found := false
	for _, event := range listed.Events {
		if event.Event == "session_failed" && event.RuntimeName == "Events test runtime" {
			found = true
		}
	}
	if !found {
		t.Fatalf("inserted session_failed event missing from list: %+v", listed.Events)
	}

	// Admin-only clear.
	testutil.Call(t, testHandler.ClearMirrorEvents,
		mirrorEventsRequest(t, http.MethodDelete),
	).Want(http.StatusOK)

	var after mirrorEventsWire
	testutil.Call(t, testHandler.ListMirrorEvents,
		mirrorEventsRequest(t, http.MethodGet),
	).Want(http.StatusOK).JSON(&after)
	if len(after.Events) != 0 {
		t.Fatalf("events after clear = %d, want 0", len(after.Events))
	}
}

func TestViewerNotificationsDefaultPersistsTrue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	h := mirrorNetworkTestHandler(t)
	t.Cleanup(func() {
		testutil.Call(t, h.UpdateWorkspaceMirrorNetwork,
			mirrorNetworkRequest(t, http.MethodPatch, map[string]any{"mode": "builtin"}),
		).Want(http.StatusOK)
	})

	// Explicit opt-out.
	testutil.Call(t, h.UpdateWorkspaceMirrorNetwork,
		mirrorNetworkRequest(t, http.MethodPatch, map[string]any{
			"mode":                         "builtin",
			"viewer_notifications_enabled": false,
		}),
	).Want(http.StatusOK)

	var disabled struct {
		ViewerNotificationsEnabled bool `json:"viewer_notifications_enabled"`
	}
	testutil.Call(t, h.GetWorkspaceMirrorNetwork,
		mirrorNetworkRequest(t, http.MethodGet, nil),
	).Want(http.StatusOK).JSON(&disabled)
	if disabled.ViewerNotificationsEnabled {
		t.Fatal("viewer_notifications_enabled = true, want false after opt-out")
	}

	// Re-enable.
	testutil.Call(t, h.UpdateWorkspaceMirrorNetwork,
		mirrorNetworkRequest(t, http.MethodPatch, map[string]any{
			"mode":                         "builtin",
			"viewer_notifications_enabled": true,
		}),
	).Want(http.StatusOK)
	var enabled struct {
		ViewerNotificationsEnabled bool `json:"viewer_notifications_enabled"`
	}
	testutil.Call(t, h.GetWorkspaceMirrorNetwork,
		mirrorNetworkRequest(t, http.MethodGet, nil),
	).Want(http.StatusOK).JSON(&enabled)
	if !enabled.ViewerNotificationsEnabled {
		t.Fatal("viewer_notifications_enabled = false, want true after opt-in")
	}
}
