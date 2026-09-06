// Package notificationbot delivers personal inbox notifications to explicit
// user-configured destinations, independently of conversational agent bots.
package notificationbot

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var ErrConfig = errors.New("invalid notification bot configuration")

// Credentials are encrypted together at rest and never returned by read APIs.
type Credentials struct {
	WebhookURL string `json:"webhook_url"`
	Secret     string `json:"secret"`
	BotToken   string `json:"bot_token"`
	ChatID     string `json:"chat_id"`
}

// Config is a parsed, platform-pinned outbound destination.
type Config struct {
	platform    string
	endpoint    *url.URL
	credentials Credentials
}

var telegramToken = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
var telegramChat = regexp.MustCompile(`^(-?[0-9]+|@[A-Za-z0-9_]+)$`)
var hookPath = regexp.MustCompile(`^/open-apis/bot/v2/hook/[A-Za-z0-9-]+$`)
var slackPath = regexp.MustCompile(`^/services/[A-Za-z0-9]+/[A-Za-z0-9]+/[A-Za-z0-9_-]+$`)

// ParseConfig accepts only official HTTPS delivery endpoints. Errors deliberately
// exclude input values because URLs and Telegram paths contain credentials.
func ParseConfig(platform string, credentials Credentials) (Config, error) {
	if len(credentials.WebhookURL) > 2048 || len(credentials.Secret) > 256 || len(credentials.BotToken) > 256 || len(credentials.ChatID) > 128 {
		return Config{}, ErrConfig
	}
	raw := strings.TrimSpace(credentials.WebhookURL)
	if platform == "telegram" {
		if !telegramToken.MatchString(credentials.BotToken) || !telegramChat.MatchString(credentials.ChatID) || raw != "" || credentials.Secret != "" {
			return Config{}, ErrConfig
		}
		raw = "https://api.telegram.org/bot" + credentials.BotToken + "/sendMessage"
	} else if credentials.BotToken != "" || credentials.ChatID != "" {
		return Config{}, ErrConfig
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.RawPath != "" || (u.Port() != "" && u.Port() != "443") {
		return Config{}, ErrConfig
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Config{}, ErrConfig
	}
	valid := false
	switch platform {
	case "wecom":
		valid = u.Hostname() == "qyapi.weixin.qq.com" && u.Path == "/cgi-bin/webhook/send" && len(query) == 1 && len(query["key"]) == 1 && query.Get("key") != "" && credentials.Secret == ""
	case "lark":
		valid = (u.Hostname() == "open.feishu.cn" || u.Hostname() == "open.larksuite.com") && hookPath.MatchString(u.Path) && u.RawQuery == ""
	case "dingtalk":
		valid = u.Hostname() == "oapi.dingtalk.com" && u.Path == "/robot/send" && len(query) == 1 && len(query["access_token"]) == 1 && query.Get("access_token") != ""
	case "slack":
		valid = u.Hostname() == "hooks.slack.com" && slackPath.MatchString(u.Path) && u.RawQuery == "" && credentials.Secret == ""
	case "telegram":
		valid = true
	default:
		return Config{}, ErrConfig
	}
	if !valid {
		return Config{}, ErrConfig
	}
	credentials.WebhookURL = strings.TrimSpace(credentials.WebhookURL)
	return Config{platform: platform, endpoint: u, credentials: credentials}, nil
}
