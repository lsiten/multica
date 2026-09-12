package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type mirrorViewerNoticeRow struct {
	Type        string
	RecipientID string
	State       string
	RuntimeName string
}

func isolatedMirrorViewerHandler(t *testing.T) *Handler {
	t.Helper()
	if testHandler == nil {
		t.Skip("database not available")
	}
	h := *testHandler
	h.Bus = events.New()
	h.MirrorViewers = mirror.NewViewerTracker()
	h.DaemonHub = daemonws.NewHub()
	return &h
}

func connectMirrorViewerDaemon(t *testing.T, h *Handler, identity daemonws.ClientIdentity) *websocket.Conn {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.DaemonHub.HandleWebSocket(w, r, identity)
	}))
	t.Cleanup(server.Close)

	wsURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse websocket url: %v", err)
	}
	wsURL.Scheme = "ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("connect daemon websocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	for h.DaemonHub.RuntimeConnectionCount(identity.RuntimeIDs[0]) == 0 {
		time.Sleep(time.Millisecond)
	}
	return conn
}

func listMirrorViewerNotices(t *testing.T, runtimeID string) []mirrorViewerNoticeRow {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT type, recipient_id, details->>'state', details->>'runtime_name'
		FROM inbox_item
		WHERE details->>'runtime_id' = $1
		ORDER BY created_at, id
	`, runtimeID)
	if err != nil {
		t.Fatalf("list mirror viewer notices: %v", err)
	}
	defer rows.Close()

	var got []mirrorViewerNoticeRow
	for rows.Next() {
		var row mirrorViewerNoticeRow
		if err := rows.Scan(&row.Type, &row.RecipientID, &row.State, &row.RuntimeName); err != nil {
			t.Fatalf("scan mirror viewer notice: %v", err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate mirror viewer notices: %v", err)
	}
	return got
}

func TestHandleDaemonMirrorViewerNotifiesOwnerOnTransitions(t *testing.T) {
	h := isolatedMirrorViewerHandler(t)
	const daemonID = "mirror-viewer-daemon"
	runtimeID := dbfx.Runtime(t, "Mirror Viewer Runtime", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"owner_id":     testUserID,
		"daemon_id":    daemonID,
		"custom_name":  "Mirror Viewer Box",
	})
	dbfx.Cleanup(t, `DELETE FROM inbox_item WHERE details->>'runtime_id' = $1`, runtimeID)

	eventsSeen := make(chan string, 2)
	h.Bus.Subscribe(protocol.EventInboxNew, func(event events.Event) {
		payload, ok := event.Payload.(map[string]any)
		if !ok {
			eventsSeen <- ""
			return
		}
		switch item := payload["item"].(type) {
		case InboxItemResponse:
			eventsSeen <- item.Type
		case map[string]any:
			itemType, _ := item["type"].(string)
			eventsSeen <- itemType
		default:
			eventsSeen <- ""
		}
	})

	identity := daemonws.ClientIdentity{
		DaemonID:     daemonID,
		WorkspaceID:  testWorkspaceID,
		WorkspaceIDs: []string{testWorkspaceID},
		RuntimeIDs:   []string{runtimeID},
	}
	connectMirrorViewerDaemon(t, h, identity)

	payload := protocol.MirrorViewerPayload{
		WorkspaceID: testWorkspaceID,
		RuntimeID:   runtimeID,
		DaemonID:    daemonID,
		Active:      true,
	}
	if err := h.HandleDaemonMirrorViewer(context.Background(), identity, payload); err != nil {
		t.Fatalf("start mirror viewer: %v", err)
	}
	if err := h.HandleDaemonMirrorViewer(context.Background(), identity, payload); err != nil {
		t.Fatalf("duplicate start mirror viewer: %v", err)
	}
	payload.Active = false
	if err := h.HandleDaemonMirrorViewer(context.Background(), identity, payload); err != nil {
		t.Fatalf("stop mirror viewer: %v", err)
	}

	notices := listMirrorViewerNotices(t, runtimeID)
	if len(notices) != 2 {
		t.Fatalf("notices = %v, want one started and one stopped", notices)
	}
	want := []mirrorViewerNoticeRow{
		{Type: protocol.InboxTypeRuntimeMirrorViewerStarted, RecipientID: testUserID, State: "started", RuntimeName: "Mirror Viewer Box"},
		{Type: protocol.InboxTypeRuntimeMirrorViewerStopped, RecipientID: testUserID, State: "ended", RuntimeName: "Mirror Viewer Box"},
	}
	for i := range want {
		if notices[i] != want[i] {
			t.Fatalf("notice %d = %+v, want %+v", i, notices[i], want[i])
		}
	}

	for i := range want {
		select {
		case got := <-eventsSeen:
			if got != want[i].Type {
				t.Fatalf("event %d = %q, want %q", i, got, want[i].Type)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("mirror viewer inbox event was not published")
		}
	}
}

func TestHandleDaemonMirrorViewerInactiveClosesOnlyThatViewerSession(t *testing.T) {
	// Given
	h := isolatedMirrorViewerHandler(t)
	sessions := mirror.NewSessionStore(mirror.SessionStoreOptions{})
	h.MirrorSessions = sessions
	const daemonID = "mirror-viewer-specific-daemon"
	runtimeID := dbfx.Runtime(t, "Mirror Viewer Specific Runtime", testutil.Cols{
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
	connectMirrorViewerDaemon(t, h, identity)

	firstIdentity := mirror.SessionIdentity{
		WorkspaceID: testWorkspaceID,
		RuntimeID:   runtimeID,
		UserID:      testUserID,
		DaemonID:    daemonID,
		ViewerID:    "viewer-1",
	}
	secondIdentity := firstIdentity
	secondIdentity.ViewerID = "viewer-2"
	answerOffer := func(viewerIdentity mirror.SessionIdentity, answerSDP string) mirror.Session {
		t.Helper()
		session, err := sessions.Create(context.Background(), mirror.CreateSessionInput{
			Identity: viewerIdentity,
			Offer: protocol.MirrorSessionDescription{
				Type: "offer",
				SDP:  "offer-sdp-" + viewerIdentity.ViewerID,
			},
		})
		if err != nil {
			t.Fatalf("create %s session: %v", viewerIdentity.ViewerID, err)
		}
		if _, err := sessions.ConsumeOffer(context.Background(), session.ID, viewerIdentity); err != nil {
			t.Fatalf("consume %s offer: %v", viewerIdentity.ViewerID, err)
		}
		if err := sessions.SetAnswer(context.Background(), session.ID, viewerIdentity, protocol.MirrorSessionDescription{
			Type: "answer",
			SDP:  answerSDP,
		}); err != nil {
			t.Fatalf("set %s answer: %v", viewerIdentity.ViewerID, err)
		}
		return session
	}
	first := answerOffer(firstIdentity, "answer-sdp-viewer-1")
	second := answerOffer(secondIdentity, "answer-sdp-viewer-2")
	viewerEvent := func(viewerID string, active bool) protocol.MirrorViewerPayload {
		return protocol.MirrorViewerPayload{
			WorkspaceID: testWorkspaceID,
			RuntimeID:   runtimeID,
			DaemonID:    daemonID,
			ViewerID:    viewerID,
			Active:      active,
		}
	}
	if err := h.HandleDaemonMirrorViewer(context.Background(), identity, viewerEvent("viewer-1", true)); err != nil {
		t.Fatalf("start first viewer: %v", err)
	}
	if err := h.HandleDaemonMirrorViewer(context.Background(), identity, viewerEvent("viewer-2", true)); err != nil {
		t.Fatalf("start second viewer: %v", err)
	}

	// When
	if err := h.HandleDaemonMirrorViewer(context.Background(), identity, viewerEvent("viewer-1", false)); err != nil {
		t.Fatalf("stop first viewer: %v", err)
	}

	// Then
	if _, err := sessions.Answer(context.Background(), first.ID, firstIdentity); !errors.Is(err, mirror.ErrSessionClosed) {
		t.Fatalf("first viewer answer after close = %v, want %v", err, mirror.ErrSessionClosed)
	}
	answer, err := sessions.Answer(context.Background(), second.ID, secondIdentity)
	if err != nil {
		t.Fatalf("second viewer answer after first viewer closed: %v", err)
	}
	if answer.SDP != "answer-sdp-viewer-2" {
		t.Fatalf("second viewer SDP = %q, want retained answer", answer.SDP)
	}
	notices := listMirrorViewerNotices(t, runtimeID)
	if len(notices) != 1 || notices[0].Type != protocol.InboxTypeRuntimeMirrorViewerStarted {
		t.Fatalf("notices after one viewer departed = %v, want only the first-viewer started notice", notices)
	}
}

func TestHandleDaemonMirrorViewerRejectsForeignDaemon(t *testing.T) {
	h := isolatedMirrorViewerHandler(t)
	const daemonID = "mirror-viewer-foreign"
	runtimeID := dbfx.Runtime(t, "Mirror Viewer Foreign Runtime", testutil.Cols{
		"workspace_id": testWorkspaceID,
		"owner_id":     testUserID,
		"daemon_id":    "expected-daemon",
	})
	dbfx.Cleanup(t, `DELETE FROM inbox_item WHERE details->>'runtime_id' = $1`, runtimeID)

	identity := daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceIDs: []string{testWorkspaceID}, RuntimeIDs: []string{runtimeID}}
	payload := protocol.MirrorViewerPayload{WorkspaceID: testWorkspaceID, RuntimeID: runtimeID, DaemonID: daemonID, Active: true}
	if err := h.HandleDaemonMirrorViewer(context.Background(), identity, payload); err == nil {
		t.Fatal("foreign daemon viewer state was accepted")
	}
	if len(listMirrorViewerNotices(t, runtimeID)) != 0 {
		t.Fatal("foreign daemon viewer state created an inbox notice")
	}
}

func TestHandleDaemonMirrorDisconnectSendsStoppedNotice(t *testing.T) {
	h := isolatedMirrorViewerHandler(t)
	const daemonID = "mirror-viewer-disconnect-daemon"
	runtimeID := dbfx.Runtime(t, "Mirror Viewer Disconnect Runtime", testutil.Cols{
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
	if err := h.HandleDaemonMirrorViewer(context.Background(), identity, protocol.MirrorViewerPayload{
		WorkspaceID: testWorkspaceID,
		RuntimeID:   runtimeID,
		DaemonID:    daemonID,
		Active:      true,
	}); err != nil {
		t.Fatalf("start mirror viewer: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close daemon connection: %v", err)
	}
	for h.DaemonHub.RuntimeConnectionCount(runtimeID) > 0 {
		time.Sleep(time.Millisecond)
	}

	h.HandleDaemonMirrorDisconnect(context.Background(), identity)

	notices := listMirrorViewerNotices(t, runtimeID)
	if len(notices) != 2 || notices[1].Type != protocol.InboxTypeRuntimeMirrorViewerStopped {
		t.Fatalf("notices after disconnect = %v, want started and stopped", notices)
	}
}

func TestHandleDaemonMirrorDisconnectClosesPendingSignalingSessions(t *testing.T) {
	// Given
	sessions := mirror.NewSessionStore(mirror.SessionStoreOptions{})
	sessionIdentity := mirror.SessionIdentity{
		WorkspaceID: testWorkspaceID,
		RuntimeID:   "runtime-pending-disconnect",
		UserID:      testUserID,
		DaemonID:    "daemon-pending-disconnect",
		ViewerID:    "viewer-pending-disconnect",
	}
	session, err := sessions.Create(context.Background(), mirror.CreateSessionInput{
		Identity: sessionIdentity,
		Offer: protocol.MirrorSessionDescription{
			Type: "offer",
			SDP:  "offer-sdp",
		},
	})
	if err != nil {
		t.Fatalf("create mirror session: %v", err)
	}
	if _, err := sessions.ConsumeOffer(context.Background(), session.ID, sessionIdentity); err != nil {
		t.Fatalf("consume mirror offer: %v", err)
	}
	h := &Handler{
		DaemonHub:      daemonws.NewHub(),
		MirrorSessions: sessions,
		MirrorViewers:  mirror.NewViewerTracker(),
	}
	daemonIdentity := daemonws.ClientIdentity{
		DaemonID:     sessionIdentity.DaemonID,
		WorkspaceIDs: []string{sessionIdentity.WorkspaceID},
		RuntimeIDs:   []string{sessionIdentity.RuntimeID},
	}

	// When
	h.HandleDaemonMirrorDisconnect(context.Background(), daemonIdentity)

	// Then
	metadata, err := sessions.MetadataByID(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("load closed session metadata: %v", err)
	}
	if metadata.State != mirror.SessionStateClosed {
		t.Fatalf("session state = %q, want %q", metadata.State, mirror.SessionStateClosed)
	}
	if _, err := sessions.Answer(context.Background(), session.ID, sessionIdentity); err == nil {
		t.Fatal("answer after disconnect unexpectedly succeeded")
	}
}
