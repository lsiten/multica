package notificationbot

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/pkg/remotemcp"
)

var ErrDelivery = errors.New("notification provider rejected the message or is unavailable")

// Sender pins outbound hosts, rejects redirects and bounds calls. Client is an
// optional test transport; production always uses the public-address guard.
type Sender struct {
	Client *http.Client
	Now    func() time.Time
}

type textContent struct {
	Content string `json:"content"`
}
type webhookText struct {
	MsgType string      `json:"msgtype"`
	Text    textContent `json:"text"`
}

// Send returns sanitized errors only: net/http errors and provider bodies can
// echo secrets in URLs, and must never reach API responses or logs.
func (s Sender) Send(ctx context.Context, config Config, message string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	endpoint := *config.endpoint
	// A byte ceiling also satisfies WeCom's UTF-8 limit and leaves room for
	// every provider's wrapper. Never split a multibyte character.
	if len(message) > 1900 {
		message = message[:1897]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
		message += "..."
	}
	var payload []byte
	var err error
	switch config.platform {
	case "wecom", "dingtalk":
		payload, err = json.Marshal(webhookText{MsgType: "text", Text: textContent{Content: message}})
		if config.platform == "dingtalk" && config.credentials.Secret != "" {
			timestamp := strconv.FormatInt(now.UnixMilli(), 10)
			mac := hmac.New(sha256.New, []byte(config.credentials.Secret))
			mac.Write([]byte(timestamp + "\n" + config.credentials.Secret))
			query := endpoint.Query()
			query.Set("timestamp", timestamp)
			query.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
			endpoint.RawQuery = query.Encode()
		}
	case "lark":
		body := struct {
			MsgType string `json:"msg_type"`
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
			Timestamp string `json:"timestamp,omitempty"`
			Sign      string `json:"sign,omitempty"`
		}{MsgType: "text"}
		// Feishu interprets XML-like mention tags even in text messages.
		body.Content.Text = strings.NewReplacer("<", "＜", ">", "＞").Replace(message)
		if config.credentials.Secret != "" {
			body.Timestamp = strconv.FormatInt(now.Unix(), 10)
			mac := hmac.New(sha256.New, []byte(body.Timestamp+"\n"+config.credentials.Secret))
			body.Sign = base64.StdEncoding.EncodeToString(mac.Sum(nil))
		}
		payload, err = json.Marshal(body)
	case "slack":
		body := struct {
			Text        string `json:"text"`
			Mrkdwn      bool   `json:"mrkdwn"`
			UnfurlLinks bool   `json:"unfurl_links"`
			UnfurlMedia bool   `json:"unfurl_media"`
		}{Text: strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(message)}
		payload, err = json.Marshal(body)
	case "telegram":
		body := struct {
			ChatID                string `json:"chat_id"`
			Text                  string `json:"text"`
			DisableWebPagePreview bool   `json:"disable_web_page_preview"`
		}{ChatID: config.credentials.ChatID, Text: message, DisableWebPagePreview: true}
		payload, err = json.Marshal(body)
	default:
		return ErrConfig
	}
	if err != nil {
		return ErrDelivery
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return ErrDelivery
	}
	request.Header.Set("Content-Type", "application/json")
	client := s.Client
	if client == nil {
		client = remotemcp.NewSecureHTTPClient(&endpoint)
		defer client.CloseIdleConnections()
	}
	response, err := client.Do(request)
	if err != nil {
		return ErrDelivery
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrDelivery
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return ErrDelivery
	}
	if config.platform == "slack" {
		if strings.TrimSpace(string(raw)) == "ok" {
			return nil
		}
		return ErrDelivery
	}
	var ack struct {
		ErrCode    *int `json:"errcode"`
		Code       *int `json:"code"`
		StatusCode *int `json:"StatusCode"`
		OK         bool `json:"ok"`
	}
	if json.Unmarshal(raw, &ack) != nil {
		return ErrDelivery
	}
	switch config.platform {
	case "wecom", "dingtalk":
		if ack.ErrCode != nil && *ack.ErrCode == 0 {
			return nil
		}
	case "lark":
		if ack.Code != nil && *ack.Code == 0 || ack.Code == nil && ack.StatusCode != nil && *ack.StatusCode == 0 {
			return nil
		}
	case "telegram":
		if ack.OK {
			return nil
		}
	default:
		return ErrConfig
	}
	return ErrDelivery
}
