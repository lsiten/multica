package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

type notificationFixtureTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (t notificationFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	request := r.Clone(r.Context())
	endpoint := *r.URL
	endpoint.Scheme, endpoint.Host = t.target.Scheme, t.target.Host
	request.URL = &endpoint
	return t.base.RoundTrip(request)
}

func notificationDeliveryFixture(t *testing.T) (*Handler, string, *atomic.Int32) {
	t.Helper()
	h := botTestHandler(t)
	id := createBotFixture(t, h)
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"errcode":0}`)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	h.NotificationBots.Sender.Client = &http.Client{Transport: notificationFixtureTransport{target: target, base: server.Client().Transport}}
	dbfx.Insert(t, "inbox_item", testutil.Cols{"workspace_id": testWorkspaceID, "recipient_type": "member", "recipient_id": testUserID, "type": "new_comment", "title": "Fixture notification"})
	return h, id, calls
}

func TestNotificationBotWorkerDeduplicatesDurableInbox(t *testing.T) {
	// Given: a real inbox row and HTTP receiver; no event-bus callback needed.
	h, id, calls := notificationDeliveryFixture(t)
	// When: repeated recovery scans see the same row.
	if err := h.NotificationBots.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.NotificationBots.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Then
	if calls.Load() != 1 {
		t.Fatalf("got %d deliveries", calls.Load())
	}
	if dbfx.Count(t, `SELECT count(*) FROM notification_bot_delivery WHERE bot_id = $1 AND completed_at IS NOT NULL`, id) != 1 {
		t.Fatal("delivery not durably acknowledged")
	}
}

func TestNotificationBotWorkerHonorsMutePreferences(t *testing.T) {
	// Given
	h, _, calls := notificationDeliveryFixture(t)
	dbfx.InsertNoID(t, "notification_preference", testutil.Cols{"workspace_id": testWorkspaceID, "user_id": testUserID, "preferences": `{"comments":"muted"}`}, "workspace_id=$1 AND user_id=$2", testWorkspaceID, testUserID)
	// When
	if err := h.NotificationBots.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Then
	if calls.Load() != 0 {
		t.Fatal("muted notification sent")
	}
}

func TestNotificationBotWorkerSkipsDisabledQueuedBot(t *testing.T) {
	// Given: disable after enqueue, before the worker sends.
	h, id, calls := notificationDeliveryFixture(t)
	if err := h.Queries.EnqueueNotificationBotDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE notification_bot SET is_enabled=false WHERE id=$1`, id)
	// When
	if err := h.NotificationBots.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Then
	if calls.Load() != 0 {
		t.Fatal("disabled bot received a queued message")
	}
}

func TestNotificationBotWorkerSkipsDepartedOwner(t *testing.T) {
	// Given: simulate an orphaned bot owned by a non-member.
	h, id, calls := notificationDeliveryFixture(t)
	if err := h.Queries.EnqueueNotificationBotDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	other := dbfx.User(t, "Departed bot owner", "departed-notification@example.com")
	dbfx.Exec(t, `UPDATE notification_bot SET user_id=$2 WHERE id=$1`, id, other)
	dbfx.Exec(t, `UPDATE inbox_item SET recipient_id=$2 WHERE id IN (SELECT inbox_id FROM notification_bot_delivery WHERE bot_id=$1)`, id, other)
	// When
	if err := h.NotificationBots.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Then
	if calls.Load() != 0 {
		t.Fatal("departed owner received a queued message")
	}
}
