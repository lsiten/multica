package handler

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/notificationbot"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func botTestHandler(t *testing.T) *Handler {
	t.Helper()
	box, err := secretbox.New(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{Queries: testHandler.Queries, NotificationBots: notificationbot.NewWorker(testHandler.Queries, box, "https://example.com")}
}

func botRequest(t *testing.T, method, id string, body any) *http.Request {
	t.Helper()
	req := testutil.WithHeaders(testutil.JSONRequest(method, "/api/notification-bots", body), "X-User-ID", testUserID)
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{WorkspaceID: parseUUID(testWorkspaceID), UserID: parseUUID(testUserID)})
	if err != nil {
		t.Fatal(err)
	}
	req = req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, member))
	if id != "" {
		req = testutil.WithURLParams(req, "id", id)
	}
	return req
}

func createBotFixture(t *testing.T, h *Handler) string {
	t.Helper()
	var response notificationBotResponse
	testutil.Call(t, h.SaveNotificationBot, botRequest(t, http.MethodPost, "", saveNotificationBotRequest{Name: "Fixture", Platform: "wecom", IsEnabled: true, Credentials: &notificationbot.Credentials{WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-fixture"}})).Want(http.StatusOK).JSON(&response)
	id := util.UUIDToString(response.ID)
	dbfx.Cleanup(t, `DELETE FROM notification_bot WHERE id = $1`, id)
	dbfx.Cleanup(t, `DELETE FROM notification_bot_delivery WHERE bot_id = $1`, id)
	return id
}

func TestNotificationBotCredentialsPreservedAndRedacted(t *testing.T) {
	// Given
	h := botTestHandler(t)
	id := createBotFixture(t, h)
	before, err := h.Queries.GetNotificationBot(context.Background(), db.GetNotificationBotParams{ID: parseUUID(id), WorkspaceID: parseUUID(testWorkspaceID), UserID: parseUUID(testUserID)})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(before.Credentials, []byte("secret-fixture")) {
		t.Fatal("stored plaintext credentials")
	}
	// When: update only public fields.
	response := testutil.Call(t, h.SaveNotificationBot, botRequest(t, http.MethodPut, id, saveNotificationBotRequest{Name: "Renamed", Platform: "wecom", IsEnabled: false})).Want(http.StatusOK)
	// Then
	if strings.Contains(response.Body.String(), "credentials") || strings.Contains(response.Body.String(), "secret-fixture") {
		t.Fatal("credentials leaked")
	}
	after, err := h.Queries.GetNotificationBot(context.Background(), db.GetNotificationBotParams{ID: parseUUID(id), WorkspaceID: parseUUID(testWorkspaceID), UserID: parseUUID(testUserID)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Credentials, after.Credentials) || after.IsEnabled || after.Name != "Renamed" {
		t.Fatal("public edit changed credentials or lost fields")
	}
}

func TestNotificationBotRejectsAnotherOwner(t *testing.T) {
	// Given
	h := botTestHandler(t)
	id := createBotFixture(t, h)
	other := dbfx.User(t, "Other bot owner", "other-notification-bot@example.com")
	dbfx.Member(t, testWorkspaceID, other, "member")
	for _, action := range []struct {
		method  string
		handler http.HandlerFunc
		body    any
	}{
		{http.MethodPut, h.SaveNotificationBot, saveNotificationBotRequest{Name: "Stolen", Platform: "wecom"}},
		{http.MethodDelete, h.DeleteNotificationBot, nil},
		{http.MethodPost, h.TestNotificationBot, nil},
	} {
		// When / Then
		req := botRequest(t, action.method, id, action.body)
		req.Header.Set("X-User-ID", other)
		testutil.Call(t, action.handler, req).Want(http.StatusNotFound)
	}
}

func TestNotificationBotDeletionRemovesDeliveries(t *testing.T) {
	// Given
	h := botTestHandler(t)
	id := createBotFixture(t, h)
	dbfx.Insert(t, "notification_bot_delivery", testutil.Cols{"bot_id": id, "inbox_id": id})
	// When
	testutil.Call(t, h.DeleteNotificationBot, botRequest(t, http.MethodDelete, id, nil)).Want(http.StatusNoContent)
	// Then: dependent cleanup is an application-layer contract, without FKs.
	if dbfx.Count(t, `SELECT count(*) FROM notification_bot_delivery WHERE bot_id = $1`, id) != 0 {
		t.Fatal("delivery row orphaned")
	}
}
