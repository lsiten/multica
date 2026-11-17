package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/mobilepush"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type pushRecorder struct {
	notifications []mobilepush.Notification
	err           error
}

func (p *pushRecorder) Send(_ context.Context, notification mobilepush.Notification) error {
	p.notifications = append(p.notifications, notification)
	return p.err
}

func TestMobilePushDeviceRegistrationOwnership(t *testing.T) {
	ctx := context.Background()
	user := dbfx.User(t, "Push owner", uuid.NewString()+"@push.test")
	other := dbfx.User(t, "Other push owner", uuid.NewString()+"@push.test")
	token := strings.ReplaceAll(uuid.NewString(), "-", "")
	device := mobilePushDeviceRequest{Token: token, Topic: "test.app", Environment: "sandbox"}
	dbfx.Cleanup(t, "DELETE FROM mobile_push_device WHERE token = $1", token)
	h := &Handler{Queries: testHandler.Queries, MobilePush: mobilepush.NewWorker(testHandler.Queries, map[string]mobilepush.Sender{"sandbox": &pushRecorder{}}, []string{"test.app"})}
	request := func(method, userID string, body mobilePushDeviceRequest) *http.Request {
		return testutil.WithHeaders(testutil.JSONRequest(method, "/api/mobile-push/devices", body), "X-User-ID", userID)
	}
	testutil.Call(t, h.RegisterMobilePushDevice, request(http.MethodPut, user, device)).Want(http.StatusOK)
	first, err := h.Queries.RegisterMobilePushDevice(ctx, db.RegisterMobilePushDeviceParams{UserID: parseUUID(user), Token: token, Topic: device.Topic, Environment: device.Environment})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, h.DeleteMobilePushDevice, request(http.MethodDelete, other, device)).Want(http.StatusNoContent)
	var count int
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM mobile_push_device WHERE id = $1", first.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("another account removed the registered device")
	}
	testutil.Call(t, h.RegisterMobilePushDevice, request(http.MethodPut, other, device)).Want(http.StatusOK)
	var owner string
	if err := testPool.QueryRow(ctx, "SELECT user_id FROM mobile_push_device WHERE id = $1", first.ID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != other {
		t.Fatalf("device still belongs to old account %s", owner)
	}
	// A late invalid-token response from the previous registration must not remove its replacement.
	if err := h.Queries.InvalidateMobilePushDevice(ctx, db.InvalidateMobilePushDeviceParams{ID: first.ID, UpdatedAt: first.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, h.DeleteMobilePushDevice, request(http.MethodDelete, user, device)).Want(http.StatusNoContent)
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM mobile_push_device WHERE id = $1", first.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("stale registration cleanup removed the new account's device")
	}
	testutil.Call(t, h.DeleteMobilePushDevice, request(http.MethodDelete, other, device)).Want(http.StatusNoContent)
}

func TestMobilePushRegistrationValidation(t *testing.T) {
	h := &Handler{Queries: testHandler.Queries}
	for _, tc := range []struct {
		name, token, topic, environment string
		status                          int
	}{
		{"disabled", "aabb", "test.app", "sandbox", http.StatusServiceUnavailable},
		{"invalid token", "not-hex", "test.app", "sandbox", http.StatusBadRequest},
		{"invalid environment", "aabb", "test.app", "development", http.StatusBadRequest},
		{"empty topic", "aabb", "", "sandbox", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := testutil.WithHeaders(testutil.JSONRequest(http.MethodPut, "/api/mobile-push/devices", mobilePushDeviceRequest{tc.token, tc.topic, tc.environment}), "X-User-ID", testUserID)
			testutil.Call(t, h.RegisterMobilePushDevice, req).Want(tc.status)
		})
	}
}
