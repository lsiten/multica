package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestCreateMirrorSessionConsumesOfferBeforeDaemonAnswer(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	const daemonID = "mirror-handler-daemon"
	runtimeID := dbfx.Runtime(t, "Mirror handler runtime", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"owner_id":     testUserID,
		"daemon_id":    daemonID,
		"provider":     "mirror-handler",
		"device_info":  "Mirror handler runtime",
		"metadata":     testutil.Raw(`'{"capabilities":["screen-mirror-v1"]}'::jsonb`),
		"visibility":   "private",
		"status":       "online",
	})

	daemonHub := daemonws.NewHub()
	daemonHub.SetMirrorAnswerHandler(testHandler.HandleDaemonMirrorAnswer)
	previousHub := testHandler.DaemonHub
	testHandler.DaemonHub = daemonHub
	t.Cleanup(func() { testHandler.DaemonHub = previousHub })

	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := middleware.WithDaemonContext(r.Context(), testWorkspaceID, daemonID)
		testHandler.DaemonWebSocket(w, r.WithContext(ctx))
	}))
	defer wsServer.Close()
	wsURL, err := url.Parse(wsServer.URL)
	if err != nil {
		t.Fatalf("parse websocket url: %v", err)
	}
	wsURL.Scheme = "ws"
	wsURL.RawQuery = "runtime_id=" + url.QueryEscape(runtimeID)
	conn, dialResp, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		body := ""
		if dialResp != nil {
			defer dialResp.Body.Close()
			data, _ := io.ReadAll(dialResp.Body)
			body = string(data)
		}
		t.Fatalf("connect daemon websocket: %v: %s", err, body)
	}
	defer conn.Close()
	if got := testHandler.DaemonHub.RuntimeConnectionCount(runtimeID); got != 1 {
		t.Fatalf("runtime connection count = %d, want 1", got)
	}

	body := map[string]any{
		"viewer_id": "viewer-1",
		"offer": map[string]string{
			"type": "offer",
			"sdp":  "offer-sdp",
		},
	}
	req := withURLParam(newRequest("POST", "/api/runtimes/"+runtimeID+"/mirror/sessions", body), "runtimeId", runtimeID)
	createResp := testutil.Call(t, testHandler.CreateMirrorSession, req).Want(http.StatusCreated)
	var created struct {
		ID string `json:"id"`
	}
	createResp.JSON(&created)
	if created.ID == "" {
		t.Fatal("created session id is empty")
	}

	identity := daemonws.ClientIdentity{
		DaemonID:     daemonID,
		UserID:       testUserID,
		WorkspaceID:  testWorkspaceID,
		WorkspaceIDs: []string{testWorkspaceID},
		RuntimeIDs:   []string{runtimeID},
	}
	payload := protocol.MirrorAnswerPayload{
		SessionID:   created.ID,
		WorkspaceID: testWorkspaceID,
		RuntimeID:   runtimeID,
		UserID:      testUserID,
		DaemonID:    daemonID,
		ViewerID:    "viewer-1",
		Answer:      protocol.MirrorSessionDescription{Type: "answer", SDP: "answer-sdp"},
	}
	if err := testHandler.HandleDaemonMirrorAnswer(context.Background(), identity, payload); err != nil {
		t.Fatalf("daemon answer rejected before it reached the session store: %v", err)
	}
}

func TestDaemonMirrorAnswerFailureOverWebSocketExposesFailedSessionWithoutAnswer(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	const daemonID = "mirror-failure-daemon"
	runtimeID := dbfx.Runtime(t, "Mirror failure runtime", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"owner_id":     testUserID,
		"daemon_id":    daemonID,
		"provider":     "mirror-failure",
		"device_info":  "Mirror failure runtime",
		"metadata":     testutil.Raw(`'{"capabilities":["screen-mirror-v1"]}'::jsonb`),
		"visibility":   "private",
		"status":       "online",
	})

	daemonHub := daemonws.NewHub()
	daemonHub.SetMirrorAnswerFailureHandler(testHandler.HandleDaemonMirrorAnswerFailure)
	previousHub := testHandler.DaemonHub
	testHandler.DaemonHub = daemonHub
	t.Cleanup(func() { testHandler.DaemonHub = previousHub })

	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := middleware.WithDaemonContext(r.Context(), testWorkspaceID, daemonID)
		testHandler.DaemonWebSocket(w, r.WithContext(ctx))
	}))
	defer wsServer.Close()
	wsURL, err := url.Parse(wsServer.URL)
	if err != nil {
		t.Fatalf("parse websocket url: %v", err)
	}
	wsURL.Scheme = "ws"
	wsURL.RawQuery = "runtime_id=" + url.QueryEscape(runtimeID)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("connect daemon websocket: %v", err)
	}
	defer conn.Close()

	createBody := map[string]any{
		"viewer_id": "viewer-1",
		"offer": map[string]string{
			"type": "offer",
			"sdp":  "offer-sdp",
		},
	}
	createReq := withURLParam(
		newRequest(http.MethodPost, "/api/runtimes/"+runtimeID+"/mirror/sessions", createBody),
		"runtimeId", runtimeID,
	)
	createResp := testutil.Call(t, testHandler.CreateMirrorSession, createReq).Want(http.StatusCreated)
	var created struct {
		ID string `json:"id"`
	}
	createResp.JSON(&created)

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set offer read deadline: %v", err)
	}
	_, rawOfferFrame, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read mirror offer: %v", err)
	}
	var offerMsg protocol.Message
	if err := json.Unmarshal(rawOfferFrame, &offerMsg); err != nil {
		t.Fatalf("decode mirror offer frame: %v", err)
	}
	var offer protocol.MirrorOfferPayload
	if err := json.Unmarshal(offerMsg.Payload, &offer); err != nil {
		t.Fatalf("decode mirror offer payload: %v", err)
	}
	failure := protocol.MirrorAnswerFailurePayload{
		SessionID:   offer.SessionID,
		WorkspaceID: offer.WorkspaceID,
		RuntimeID:   offer.RuntimeID,
		UserID:      offer.UserID,
		DaemonID:    offer.DaemonID,
		ViewerID:    offer.ViewerID,
		Reason:      protocol.MirrorAnswerFailureNoDisplay,
		ExpiresAt:   offer.ExpiresAt,
	}
	rawFailure, err := json.Marshal(failure)
	if err != nil {
		t.Fatalf("marshal failure payload: %v", err)
	}
	failureFrame, err := json.Marshal(protocol.Message{
		Type:    protocol.EventMirrorAnswerFailure,
		Payload: rawFailure,
	})
	if err != nil {
		t.Fatalf("marshal failure frame: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, failureFrame); err != nil {
		t.Fatalf("write mirror answer failure: %v", err)
	}

	getReq := withURLParams(
		newRequest(http.MethodGet, "/api/runtimes/"+runtimeID+"/mirror/sessions/"+created.ID, nil),
		"runtimeId", runtimeID, "sessionId", created.ID,
	)
	deadline := time.Now().Add(2 * time.Second)
	for {
		var got struct {
			State         string          `json:"state"`
			FailureReason string          `json:"failure_reason"`
			Answer        json.RawMessage `json:"answer"`
		}
		testutil.Call(t, testHandler.GetMirrorSession, getReq).Want(http.StatusOK).JSON(&got)
		if got.State == "failed" {
			if got.FailureReason != protocol.MirrorAnswerFailureNoDisplay {
				t.Fatalf("failure reason = %q, want %q", got.FailureReason, protocol.MirrorAnswerFailureNoDisplay)
			}
			if len(got.Answer) != 0 && string(got.Answer) != "null" {
				t.Fatalf("failed session exposed answer: %s", got.Answer)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session = %+v, want failed/no-display", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
