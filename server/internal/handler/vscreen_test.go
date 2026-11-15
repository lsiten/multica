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

func TestMirrorHTTPSourceCatalogIsReadOnly(t *testing.T) {
	// Given: database-backed runtime and a real authenticated daemon WebSocket.
	const daemonID = "vscreen-http-daemon"
	runtimeID := dbfx.Runtime(t, "Vscreen HTTP", testutil.Cols{"workspace_id": testWorkspaceID, "owner_id": testUserID, "daemon_id": daemonID, "provider": "vscreen-test", "device_info": "test", "visibility": "private", "status": "online", "metadata": testutil.Raw(`'{"capabilities":["screen-mirror-video-v2","mirror-viewer-grant-v1"]}'::jsonb`)})
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
		var query protocol.MirrorSourcesQuery
		if err := json.Unmarshal(frame.Payload, &query); err != nil {
			daemonDone <- err
			return
		}
		sources := []protocol.MirrorSourceDescriptor{}
		for _, kind := range []protocol.MirrorSourceKind{protocol.MirrorSourcePhysical, protocol.MirrorSourceSystem} {
			sources = append(sources, protocol.MirrorSourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: protocol.ResourceKey{BackendIdentity: "http://localhost:18234", WorkspaceID: testWorkspaceID, RuntimeID: runtimeID, UID: 501, DisplayID: uint32(len(sources) + 1)}, Source: protocol.MirrorSource{Kind: kind, SourceID: "display:" + string(kind)}, NativeEpoch: "native", Generation: "display", Primary: kind == protocol.MirrorSourcePhysical}, DisplayID: uint32(len(sources) + 1), Name: string(kind), Width: 1920, Height: 1080, Scale: 1, GeometryRevision: 1})
		}
		raw, err := json.Marshal(protocol.MirrorSourcesResult{RuntimeEnvelope: query.RuntimeEnvelope, Sources: sources})
		if err != nil {
			daemonDone <- err
			return
		}
		daemonDone <- conn.WriteJSON(protocol.Message{Type: protocol.EventMirrorSourcesResult, Payload: raw})
	}()
	// When: the runtime read gate queries a fresh source catalog.
	req := withURLParam(newRequest(http.MethodGet, "/api/runtimes/"+runtimeID+"/mirror/sources", nil), "runtimeId", runtimeID)
	var catalog protocol.MirrorSourcesResult
	testutil.Call(t, testHandler.GetMirrorSources, req).Want(http.StatusOK).JSON(&catalog)
	// Then: only physical and system bindings reach the read-only HTTP boundary.
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
}
