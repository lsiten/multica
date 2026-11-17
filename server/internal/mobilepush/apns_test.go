package mobilepush

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type localAPNsTransport struct {
	host string
	base http.RoundTripper
}

func (transport localAPNsTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	forward := request.Clone(request.Context())
	target := *request.URL
	target.Host = transport.host
	forward.URL = &target
	return transport.base.RoundTrip(forward)
}

func testAPNsSender(t *testing.T, handler http.HandlerFunc) (*APNsSender, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Timeout = 5 * time.Second
	client.Transport = localAPNsTransport{host: endpoint.Host, base: client.Transport}
	sender, err := NewAPNsSender(Credentials{
		TeamID: "TEAM123456", KeyID: "KEY1234567",
		PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	return sender, key
}

func TestAPNsSendsNativeHTTP2Notification(t *testing.T) {
	type receivedRequest struct {
		protocol int
		path     string
		headers  http.Header
		payload  pushPayload
	}
	requests := make(chan receivedRequest, 1)
	sender, key := testAPNsSender(t, func(w http.ResponseWriter, r *http.Request) {
		var payload pushPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		requests <- receivedRequest{protocol: r.ProtoMajor, path: r.URL.Path, headers: r.Header.Clone(), payload: payload}
		w.WriteHeader(http.StatusOK)
	})

	err := sender.Send(context.Background(), Notification{
		DeviceToken: "012345abcdef", Topic: "ai.multica.mobile", Environment: "sandbox",
		InboxID: "inbox-1", WorkspaceSlug: "team", RecipientID: "member-1", Title: "Approval requested", Body: "Review the task", Type: "human_action_requested",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := <-requests
	if request.protocol != 2 || request.path != "/3/device/012345abcdef" {
		t.Fatalf("wrong native transport: protocol=%d path=%q", request.protocol, request.path)
	}
	if request.headers.Get("apns-push-type") != "alert" || request.headers.Get("apns-topic") != "ai.multica.mobile" || request.headers.Get("apns-collapse-id") != "inbox-1" {
		t.Fatal("missing APNs delivery headers")
	}
	provider, err := jwt.Parse(strings.TrimPrefix(request.headers.Get("Authorization"), "bearer "), func(token *jwt.Token) (any, error) { return &key.PublicKey, nil }, jwt.WithValidMethods([]string{"ES256"}))
	if err != nil || !provider.Valid || provider.Header["kid"] != "KEY1234567" {
		t.Fatalf("invalid APNs provider signature: %v", err)
	}
	issuer, err := provider.Claims.GetIssuer()
	if err != nil || issuer != "TEAM123456" {
		t.Fatalf("wrong provider issuer: %q, %v", issuer, err)
	}
	if request.payload.APS.Alert.Title != "Approval requested" || request.payload.RecipientID != "member-1" || request.payload.WorkspaceSlug != "team" || request.payload.InboxID != "inbox-1" {
		t.Fatalf("incorrect notification content: %+v", request.payload)
	}
}

func TestAPNsClassifiesDeviceInvalidationAndRetries(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		reason  string
		invalid bool
		retry   bool
	}{
		{"unregistered", 410, "Unregistered", true, false},
		{"bad token", 400, "BadDeviceToken", true, false},
		{"rate limit", 429, "TooManyRequests", false, true},
		{"unavailable", 503, "ServiceUnavailable", false, true},
		{"expired provider", 403, "ExpiredProviderToken", false, true},
		{"invalid credentials", 403, "InvalidProviderToken", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			sender, _ := testAPNsSender(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				if err := json.NewEncoder(w).Encode(struct {
					Reason string `json:"reason"`
				}{Reason: test.reason}); err != nil {
					t.Error(err)
				}
			})
			err := sender.Send(context.Background(), Notification{DeviceToken: "abcdef", Topic: "ai.multica.mobile", Environment: "production", Title: "Update"})
			var failure *ResponseError
			if !errors.As(err, &failure) || failure.InvalidDevice() != test.invalid || failure.Retryable() != test.retry {
				t.Fatalf("wrong delivery result: %v", err)
			}
		})
	}
}

func TestAPNsCancellationDoesNotExposeDeviceToken(t *testing.T) {
	sender, _ := testAPNsSender(t, func(w http.ResponseWriter, r *http.Request) { t.Error("cancelled request reached the wire") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := sender.Send(ctx, Notification{DeviceToken: "abcdef123456", Topic: "ai.multica.mobile", Environment: "production", Title: "Update"})
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "abcdef123456") {
		t.Fatalf("wrong cancellation result: %v", err)
	}
}

func TestAPNsProviderTokenIsReusedAndRefreshedBeforeExpiry(t *testing.T) {
	sender, _ := testAPNsSender(t, func(w http.ResponseWriter, r *http.Request) {})
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	first, err := sender.authorization(now)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := sender.authorization(now.Add(49 * time.Minute))
	if err != nil || reused != first {
		t.Fatalf("provider token rotated too early: %v", err)
	}
	refreshed, err := sender.authorization(now.Add(50 * time.Minute))
	if err != nil || refreshed == first {
		t.Fatalf("provider token was not renewed: %v", err)
	}
}
