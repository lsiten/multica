package notificationbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSenderChecksProviderAcknowledgement(t *testing.T) {
	// Given: HTTP 200 alone is not a successful provider acknowledgement.
	for _, response := range []string{`{}`, `{"errcode":93000}`, `invalid`, `{"errcode":0}`} {
		t.Run(response, func(t *testing.T) {
			config, err := ParseConfig("wecom", Credentials{WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=fixture"})
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var payload struct {
					MsgType string `json:"msgtype"`
					Text    struct {
						Content string `json:"content"`
					} `json:"text"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload.MsgType != "text" || payload.Text.Content != "fixture" {
					t.Fatalf("unexpected payload: %+v", payload)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
			})}
			// When
			err = (Sender{Client: client, Now: func() time.Time { return time.Unix(1700000000, 0) }}).Send(context.Background(), config, "fixture")
			// Then
			if (err == nil) != (response == `{"errcode":0}`) {
				t.Fatalf("unexpected acknowledgement result: %v", err)
			}
		})
	}
}

// Provider timeouts and rate limits remain retryable alongside server failures.
func TestSenderClassifiesPermanentErrors(t *testing.T) {
	config, err := ParseConfig("telegram", Credentials{BotToken: "123:fixture", ChatID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []int{400, 401, 403, 404} {
		t.Run(fmt.Sprintf("status-%d", status), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
			})}
			err := (Sender{Client: client}).Send(context.Background(), config, "fixture")
			if !errors.Is(err, ErrPermanent) {
				t.Fatalf("status %d: got %v, want ErrPermanent", status, err)
			}
		})
	}
	for _, status := range []int{408, 429, 500, 502, 503} {
		t.Run(fmt.Sprintf("status-%d", status), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
			})}
			err := (Sender{Client: client}).Send(context.Background(), config, "fixture")
			if !errors.Is(err, ErrDelivery) {
				t.Fatalf("status %d: got %v, want ErrDelivery (retryable)", status, err)
			}
		})
	}
}
