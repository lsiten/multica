package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenHTTPSourceCatalogAndOwnerCommand(t *testing.T) {
	// Given: database-backed runtime and a real authenticated daemon WebSocket.
	const daemonID = "vscreen-http-daemon"
	runtimeID := dbfx.Runtime(t, "Vscreen HTTP", testutil.Cols{"workspace_id": testWorkspaceID, "owner_id": testUserID, "daemon_id": daemonID, "provider": "vscreen-test", "device_info": "test", "visibility": "private", "status": "online", "metadata": testutil.Raw(`'{"capabilities":["virtual-screen-v1","mirror-viewer-grant-v1"]}'::jsonb`)})
	hub := daemonws.NewHub()
	previous := testHandler.DaemonHub
	testHandler.DaemonHub = hub
	t.Cleanup(func() { testHandler.DaemonHub = previous })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: daemonID, UserID: testUserID, WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	daemonDone := make(chan error, 1)
	go func() {
		var frame protocol.Message
		if err := conn.ReadJSON(&frame); err != nil {
			daemonDone <- err
			return
		}
		var query protocol.VscreenQuery
		if err := json.Unmarshal(frame.Payload, &query); err != nil {
			daemonDone <- err
			return
		}
		sources := []protocol.VscreenSourceDescriptor{}
		for _, kind := range []protocol.MirrorSourceKind{protocol.MirrorSourceVirtual, protocol.MirrorSourcePhysical} {
			sources = append(sources, protocol.VscreenSourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: protocol.ResourceKey{BackendIdentity: "http://localhost:18234", WorkspaceID: testWorkspaceID, RuntimeID: runtimeID, UID: 501}, Source: protocol.MirrorSource{Kind: kind, SourceID: string(kind)}, NativeEpoch: "native", Generation: "display", Primary: kind == protocol.MirrorSourcePhysical}, Name: string(kind), Width: 1920, Height: 1080, Scale: 1, GeometryRevision: 1})
		}
		raw, err := json.Marshal(protocol.VscreenQueryResult{VscreenEnvelope: query.VscreenEnvelope, Sources: sources})
		if err != nil {
			daemonDone <- err
			return
		}
		daemonDone <- conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenQueryResult, Payload: raw})
	}()
	// When: the runtime read gate queries a fresh source catalog.
	req := withURLParam(newRequest(http.MethodGet, "/api/runtimes/"+runtimeID+"/mirror/sources", nil), "runtimeId", runtimeID)
	var catalog protocol.VscreenQueryResult
	testutil.Call(t, testHandler.GetMirrorSources, req).Want(http.StatusOK).JSON(&catalog)
	// Then: exact virtual and physical bindings reach the HTTP boundary.
	if err := <-daemonDone; err != nil {
		t.Fatal(err)
	}
	if len(catalog.Sources) != 2 || catalog.RuntimeID != runtimeID || catalog.DaemonGeneration == "" {
		t.Fatalf("catalog=%+v", catalog)
	}
	raw, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("HTTP sources JSON: %s", raw)
	commandReq := withURLParam(newRequest(http.MethodPost, "/api/runtimes/"+runtimeID+"/vscreen/commands", map[string]string{"command_id": "enable-1", "kind": "enable"}), "runtimeId", runtimeID)
	var receipt protocol.VscreenCommandReceipt
	testutil.Call(t, testHandler.CreateVscreenCommand, commandReq).Want(http.StatusAccepted).JSON(&receipt)
	if receipt.State != protocol.VscreenReceiptPending {
		t.Fatalf("premature success: %+v", receipt)
	}
	denied := withURLParam(newRequest(http.MethodPost, "/commands", map[string]string{"command_id": "physical-1", "kind": "move_to_physical"}), "runtimeId", runtimeID)
	testutil.Call(t, testHandler.CreateVscreenCommand, denied).Want(http.StatusBadRequest)
	machine := withURLParam(newRequest(http.MethodPost, "/commands", map[string]string{"command_id": "machine-1", "kind": "enable"}), "runtimeId", runtimeID)
	machine.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.CreateVscreenCommand, machine).Want(http.StatusForbidden)
	t.Log("owner user identity with authoritative task_token actor source: HTTP 403")
}
