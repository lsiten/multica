package mobilepush

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrConfiguration = errors.New("APNs configuration is invalid")
	ErrNotification  = errors.New("APNs notification is invalid")
)

type Credentials struct {
	TeamID     string
	KeyID      string
	PrivateKey []byte
}

type Notification struct {
	DeviceToken   string
	Topic         string
	Environment   string
	ID            string
	InboxID       string
	WorkspaceSlug string
	RecipientID   string
	IssueID       string
	ChatSessionID string
	MessageID     string
	Type          string
	Title         string
	Body          string
}

type ResponseError struct {
	Status    int
	Reason    string
	Timestamp int64
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("APNs rejected notification (HTTP %d)", e.Status)
}

func (e *ResponseError) InvalidDevice() bool {
	return e.Status == http.StatusGone || e.Reason == "BadDeviceToken" || e.Reason == "DeviceTokenNotForTopic"
}

func (e *ResponseError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500 || e.Reason == "ExpiredProviderToken"
}

// Transport errors retain cancellation semantics without exposing a device token
// through net/http's URL-bearing error text.
type transportError struct{ cause error }

func (e *transportError) Error() string { return "APNs transport failed" }
func (e *transportError) Unwrap() error { return e.cause }

type APNsSender struct {
	client      *http.Client
	credentials Credentials
	key         *ecdsa.PrivateKey
	mu          sync.Mutex
	token       string
	issuedAt    time.Time
}

func NewAPNsSender(credentials Credentials, client *http.Client) (*APNsSender, error) {
	if strings.TrimSpace(credentials.TeamID) == "" || strings.TrimSpace(credentials.KeyID) == "" || client == nil || client.Timeout <= 0 {
		return nil, ErrConfiguration
	}
	key, err := jwt.ParseECPrivateKeyFromPEM(credentials.PrivateKey)
	if err != nil || key.Curve != elliptic.P256() {
		return nil, ErrConfiguration
	}
	credentials.PrivateKey = nil
	return &APNsSender{client: client, credentials: credentials, key: key}, nil
}

func (s *APNsSender) authorization(now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && !now.Before(s.issuedAt) && now.Sub(s.issuedAt) < 50*time.Minute {
		return s.token, nil
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.RegisteredClaims{
		Issuer: s.credentials.TeamID, IssuedAt: jwt.NewNumericDate(now),
	})
	token.Header["kid"] = s.credentials.KeyID
	signed, err := token.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("sign APNs provider token: %w", err)
	}
	s.token, s.issuedAt = signed, now
	return signed, nil
}

type alertPayload struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

type pushPayload struct {
	APS struct {
		Alert    alertPayload `json:"alert"`
		Sound    string       `json:"sound"`
		ThreadID string       `json:"thread-id"`
	} `json:"aps"`
	InboxID       string `json:"inbox_id"`
	WorkspaceSlug string `json:"workspace_slug"`
	RecipientID   string `json:"recipient_id"`
	IssueID       string `json:"issue_id,omitempty"`
	ChatSessionID string `json:"chat_session_id,omitempty"`
	MessageID     string `json:"message_id,omitempty"`
	Type          string `json:"type"`
}

func (s *APNsSender) Send(ctx context.Context, notification Notification) error {
	host := "https://api.push.apple.com"
	switch notification.Environment {
	case "production":
	case "sandbox":
		host = "https://api.sandbox.push.apple.com"
	default:
		return ErrNotification
	}
	collapseID := notification.InboxID
	if collapseID == "" {
		collapseID = notification.MessageID
	}
	if notification.Topic == "" || notification.DeviceToken == "" || notification.Title == "" || len(collapseID) > 64 {
		return ErrNotification
	}
	if _, err := hex.DecodeString(notification.DeviceToken); err != nil {
		return ErrNotification
	}
	payload := pushPayload{
		InboxID: notification.InboxID, WorkspaceSlug: notification.WorkspaceSlug,
		RecipientID: notification.RecipientID, IssueID: notification.IssueID, Type: notification.Type,
		ChatSessionID: notification.ChatSessionID, MessageID: notification.MessageID,
	}
	payload.APS.Alert = alertPayload{Title: notification.Title, Body: notification.Body}
	payload.APS.Sound = "default"
	payload.APS.ThreadID = notification.WorkspaceSlug
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode APNs payload: %w", err)
	}
	if len(body) > 4096 {
		return ErrNotification
	}
	now := time.Now()
	authorization, err := s.authorization(now)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/3/device/"+notification.DeviceToken, bytes.NewReader(body))
	if err != nil {
		return ErrNotification
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "bearer "+authorization)
	request.Header.Set("apns-topic", notification.Topic)
	request.Header.Set("apns-push-type", "alert")
	request.Header.Set("apns-priority", "10")
	request.Header.Set("apns-expiration", strconv.FormatInt(now.Add(24*time.Hour).Unix(), 10))
	if notification.ID != "" {
		request.Header.Set("apns-id", notification.ID)
	}
	if collapseID != "" {
		request.Header.Set("apns-collapse-id", collapseID)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return &transportError{cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		_, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		if err != nil {
			return &transportError{cause: err}
		}
		return nil
	}
	var failure struct {
		Reason    string `json:"reason"`
		Timestamp int64  `json:"timestamp"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&failure); err != nil {
		return &ResponseError{Status: response.StatusCode}
	}
	if failure.Reason == "ExpiredProviderToken" {
		s.mu.Lock()
		s.token = ""
		s.mu.Unlock()
	}
	return &ResponseError{Status: response.StatusCode, Reason: failure.Reason, Timestamp: failure.Timestamp}
}
