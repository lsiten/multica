package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestCreateAndListRuntimeMirrorPin(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := dbfx.Runtime(t, "Pinned mirror runtime", testutil.Cols{
		"provider":   "pinned_mirror",
		"visibility": "private",
	})

	createRecorder := httptest.NewRecorder()
	createReq := newRequest(http.MethodPost, "/api/pins", map[string]any{
		"item_type": "runtime_mirror",
		"item_id":   runtimeID,
	})
	testHandler.CreatePin(createRecorder, createReq)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create runtime mirror pin: expected 201, got %d: %s",
			createRecorder.Code, createRecorder.Body.String())
	}
	var pin PinnedItemResponse
	if err := json.NewDecoder(createRecorder.Body).Decode(&pin); err != nil {
		t.Fatalf("decode pin: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM pinned_item WHERE item_type = 'runtime_mirror' AND item_id = $1`, runtimeID)
	})

	listPins := func(target string) []PinnedItemResponse {
		recorder := httptest.NewRecorder()
		testHandler.ListPins(recorder, newRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("list pins %q: expected 200, got %d: %s",
				target, recorder.Code, recorder.Body.String())
		}
		var pins []PinnedItemResponse
		if err := json.NewDecoder(recorder.Body).Decode(&pins); err != nil {
			t.Fatalf("decode pins: %v", err)
		}
		return pins
	}

	if pinsContain(listPins("/api/pins"), pin.ID) {
		t.Fatal("runtime mirror pin leaked to the legacy pin list")
	}
	if pinsContain(listPins("/api/pins?include=view"), pin.ID) {
		t.Fatal("runtime mirror pin leaked behind the view capability only")
	}
	if !pinsContain(listPins("/api/pins?include=runtime_mirror"), pin.ID) {
		t.Fatal("runtime mirror pin missing with its capability opt-in")
	}

	otherUserID := createSecondWorkspaceMember(t)
	foreignRecorder := httptest.NewRecorder()
	foreignReq := newRequest(http.MethodPost, "/api/pins", map[string]any{
		"item_type": "runtime_mirror",
		"item_id":   runtimeID,
	})
	foreignReq.Header.Set("X-User-ID", otherUserID)
	testHandler.CreatePin(foreignRecorder, foreignReq)
	if foreignRecorder.Code != http.StatusNotFound {
		t.Fatalf("foreign private runtime pin: expected 404, got %d: %s",
			foreignRecorder.Code, foreignRecorder.Body.String())
	}
}

func TestUnbindAgentsAndDeleteRuntimeDeletesMirrorPin(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := dbfx.Runtime(t, "Deleted pinned mirror runtime", testutil.Cols{
		"provider": "deleted_pinned_mirror",
	})
	createRecorder := httptest.NewRecorder()
	testHandler.CreatePin(createRecorder, newRequest(http.MethodPost, "/api/pins", map[string]any{
		"item_type": "runtime_mirror",
		"item_id":   runtimeID,
	}))
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create runtime mirror pin: expected 201, got %d: %s",
			createRecorder.Code, createRecorder.Body.String())
	}

	deleteRecorder := httptest.NewRecorder()
	deleteReq := newRequest(http.MethodPost, "/api/runtimes/"+runtimeID+"/unbind-agents-and-delete", map[string]any{
		"expected_active_agent_ids": []string{},
	})
	deleteReq = withURLParam(deleteReq, "runtimeId", runtimeID)
	testHandler.UnbindAgentsAndDeleteRuntime(deleteRecorder, deleteReq)
	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("delete runtime: expected 200, got %d: %s",
			deleteRecorder.Code, deleteRecorder.Body.String())
	}

	var pinCount int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM pinned_item WHERE item_type = 'runtime_mirror' AND item_id = $1`,
		runtimeID,
	).Scan(&pinCount); err != nil {
		t.Fatalf("count runtime mirror pins: %v", err)
	}
	if pinCount != 0 {
		t.Fatalf("runtime mirror pins after delete = %d, want 0", pinCount)
	}
}

func pinsContain(pins []PinnedItemResponse, pinID string) bool {
	for _, pin := range pins {
		if pin.ID == pinID {
			return true
		}
	}
	return false
}
