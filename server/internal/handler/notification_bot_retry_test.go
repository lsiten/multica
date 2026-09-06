package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type rejectedNotificationTransport struct{}

func (rejectedNotificationTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable")), Header: make(http.Header)}, nil
}

func TestNotificationBotWorkerExhaustsRetries(t *testing.T) {
	// Given
	h, id, _ := notificationDeliveryFixture(t)
	h.NotificationBots.Sender.Client = &http.Client{Transport: rejectedNotificationTransport{}}
	// When: the provider keeps failing; advance only the durable retry deadline.
	for range 6 {
		if err := h.NotificationBots.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		dbfx.Exec(t, `UPDATE notification_bot_delivery SET next_attempt_at=now() WHERE bot_id=$1`, id)
	}
	// Then
	if dbfx.Count(t, `SELECT count(*) FROM notification_bot_delivery WHERE bot_id=$1 AND attempts=6 AND completed_at IS NOT NULL`, id) != 1 {
		t.Fatal("failed delivery did not stop after six attempts")
	}
	if dbfx.Count(t, `SELECT count(*) FROM notification_bot WHERE id=$1 AND last_error<>''`, id) != 1 {
		t.Fatal("failure not exposed in bot status")
	}
}

func TestNotificationBotClaimLeaseAndAttemptFence(t *testing.T) {
	// Given
	h, id, _ := notificationDeliveryFixture(t)
	ctx := context.Background()
	if err := h.Queries.EnqueueNotificationBotDeliveries(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := h.Queries.ClaimNotificationBotDelivery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Queries.ClaimNotificationBotDelivery(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("active lease was claimable: %v", err)
	}
	dbfx.Exec(t, `UPDATE notification_bot_delivery SET next_attempt_at=now() WHERE bot_id=$1`, id)
	second, err := h.Queries.ClaimNotificationBotDelivery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// When: the expired first attempt returns after the replacement claim.
	if err := h.Queries.FinishNotificationBotDelivery(ctx, db.FinishNotificationBotDeliveryParams{ID: first.ID, Attempts: first.Attempts, Finished: true, RetrySeconds: 60}); err != nil {
		t.Fatal(err)
	}
	// Then
	if second.Attempts != 2 || dbfx.Count(t, `SELECT count(*) FROM notification_bot_delivery WHERE bot_id=$1 AND completed_at IS NULL AND attempts=2`, id) != 1 {
		t.Fatal("stale completion overwrote replacement attempt")
	}
}
