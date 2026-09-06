package notificationbot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSenderPlatformWireContracts(t *testing.T) {
	// Given: deterministic time, fixture credentials, and protocol acknowledgements.
	for _, fixture := range []struct {
		platform    string
		credentials Credentials
		ack         string
	}{
		{"lark", Credentials{WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/fixture", Secret: "fixture-secret"}, `{"code":0}`},
		{"dingtalk", Credentials{WebhookURL: "https://oapi.dingtalk.com/robot/send?access_token=fixture", Secret: "fixture-secret"}, `{"errcode":0}`},
		{"slack", Credentials{WebhookURL: "https://hooks.slack.com/services/T123/B123/fixture"}, "ok"},
		{"telegram", Credentials{BotToken: "123:fixture", ChatID: "-100123"}, `{"ok":true}`},
	} {
		t.Run(fixture.platform, func(t *testing.T) {
			config, err := ParseConfig(fixture.platform, fixture.credentials)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body struct {
					MsgType   string          `json:"msg_type"`
					Timestamp string          `json:"timestamp"`
					Sign      string          `json:"sign"`
					Text      json.RawMessage `json:"text"`
					ChatID    string          `json:"chat_id"`
					ParseMode string          `json:"parse_mode"`
					Content   struct {
						Text string `json:"text"`
					} `json:"content"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				switch fixture.platform {
				case "lark":
					if body.MsgType != "text" || body.Timestamp != "1700000000" || body.Sign == "" || strings.Contains(body.Content.Text, "<at") {
						t.Fatalf("invalid signed Feishu body: %+v", body)
					}
				case "dingtalk":
					if r.URL.Query().Get("timestamp") != "1700000000000" || r.URL.Query().Get("sign") == "" {
						t.Fatal("missing DingTalk signature")
					}
				case "slack":
					if strings.Contains(string(body.Text), "<at") {
						t.Fatal("Slack mentions not escaped")
					}
				case "telegram":
					if body.ChatID != "-100123" || body.ParseMode != "" || r.URL.Path != "/bot123:fixture/sendMessage" {
						t.Fatal("invalid Telegram destination")
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture.ack)), Header: make(http.Header)}, nil
			})}
			// When
			err = (Sender{Client: client, Now: func() time.Time { return time.Unix(1700000000, 0) }}).Send(context.Background(), config, `<at user_id="all">everyone</at>`)
			// Then
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSenderSanitizesTransportErrors(t *testing.T) {
	// Given
	config, err := ParseConfig("telegram", Credentials{BotToken: "123:fixture", ChatID: "1"})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("secret 123:fixture") })}
	// When
	err = (Sender{Client: client}).Send(context.Background(), config, "fixture")
	// Then
	if !errors.Is(err, ErrDelivery) || strings.Contains(err.Error(), "123:fixture") {
		t.Fatalf("unsafe error: %v", err)
	}
}
