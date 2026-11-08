package notificationbot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type deliveryStore struct {
	credentials  []byte
	finished     bool
	retrySeconds int32
	recorded     bool
}

func (s *deliveryStore) QueryRow(context.Context, string, ...any) pgx.Row { return s }
func (s *deliveryStore) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected query")
}
func (s *deliveryStore) Scan(dest ...any) error {
	*dest[4].(*string) = "telegram"
	*dest[5].(*[]byte) = s.credentials
	*dest[13].(*string) = "fixture notification"
	*dest[15].(*string) = "mentioned"
	*dest[18].(*[]byte) = []byte(`{}`)
	return nil
}
func (s *deliveryStore) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(query, "-- name: FinishNotificationBotDelivery"):
		s.finished = args[1].(bool)
		s.retrySeconds = args[2].(int32)
		s.recorded = true
	case strings.Contains(query, "-- name: RecordNotificationBotResult"):
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected exec")
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func TestWorkerSchedulesTransientProviderResponses(t *testing.T) {
	now := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		status     int
		retryAfter string
		attempts   int32
		finished   bool
		seconds    int32
	}{
		{"rate limited", 429, "120", 1, false, 120},
		{"request timeout", 408, "", 1, false, 60},
		{"http date", 503, now.Add(150 * time.Second).Format(http.TimeFormat), 1, false, 150},
		{"bounded delay", 429, "999999999999", 1, false, 3600},
		{"malformed delay", 429, "secret invalid", 1, false, 60},
		{"negative delay", 429, "-1", 1, false, 60},
		{"past date", 503, now.Add(-time.Minute).Format(http.TimeFormat), 1, false, 60},
		{"backoff floor", 429, "1", 2, false, 120},
		{"permanent rejection", 401, "120", 1, true, 60},
		{"attempt ceiling", 429, "120", 6, true, 360},
	} {
		t.Run(tc.name, func(t *testing.T) {
			box, err := secretbox.New(make([]byte, secretbox.KeySize))
			if err != nil {
				t.Fatal(err)
			}
			ciphertext, err := box.Seal([]byte(`{"bot_token":"123:fixture","chat_id":"1"}`))
			if err != nil {
				t.Fatal(err)
			}
			store := &deliveryStore{credentials: ciphertext}
			sender := Sender{Now: func() time.Time { return now }, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": []string{tc.retryAfter}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})}}
			worker := NewWorker(db.New(store), box, "")
			worker.Sender = sender
			if err := worker.deliver(context.Background(), db.NotificationBotDelivery{Attempts: tc.attempts}); err != nil {
				t.Fatal(err)
			}
			if !store.recorded || store.finished != tc.finished || store.retrySeconds != tc.seconds {
				t.Fatalf("persisted delivery recorded=%v finished=%v retry_seconds=%d, want finished=%v retry_seconds=%d", store.recorded, store.finished, store.retrySeconds, tc.finished, tc.seconds)
			}
			t.Logf("HTTP %d -> persisted finished=%v retry_seconds=%d", tc.status, store.finished, store.retrySeconds)
		})
	}
}
