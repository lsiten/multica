package handler

import (
	"context"
	"testing"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func processQueuedWebhookDelivery(t *testing.T, deliveryID string) db.WebhookDelivery {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		delivery, err := testHandler.Queries.GetWebhookDelivery(ctx, parseUUID(deliveryID))
		if err != nil {
			t.Fatalf("load queued delivery: %v", err)
		}
		if delivery.Status != deliveryStatusQueued {
			return delivery
		}
		worked, err := testHandler.WebhookDeliveryWorker.ProcessNext(ctx)
		if err != nil {
			t.Fatalf("process queued delivery: %v", err)
		}
		if worked {
			continue
		}
		// Other server replicas can own the lease while this fixture is still queued.
		select {
		case <-poll.C:
		case <-ctx.Done():
			t.Fatalf("delivery %s did not settle: status=%s available_at=%v lease_token=%v lease_expires_at=%v: %v", deliveryID, delivery.Status, delivery.AvailableAt, delivery.LeaseToken, delivery.LeaseExpiresAt, ctx.Err())
		}
	}
}
