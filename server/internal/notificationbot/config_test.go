package notificationbot

import (
	"testing"
)

func TestParseConfigRejectsUnsafeEndpoints(t *testing.T) {
	// Given: platform-shaped URLs must not widen the outbound network policy.
	for _, endpoint := range []string{
		"http://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test",
		"https://qyapi.weixin.qq.com.evil.example/cgi-bin/webhook/send?key=test",
		"https://user@qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test",
		"https://qyapi.weixin.qq.com:8443/cgi-bin/webhook/send?key=test",
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test#fragment",
		"https://127.0.0.1/cgi-bin/webhook/send?key=test",
		"https://qyapi.weixin.qq.com/other?key=test",
	} {
		t.Run(endpoint, func(t *testing.T) {
			// When
			_, err := ParseConfig("wecom", Credentials{WebhookURL: endpoint})
			// Then
			if err == nil {
				t.Fatal("unsafe endpoint accepted")
			}
		})
	}
}

func TestParseConfigSupportsFivePlatforms(t *testing.T) {
	// Given
	fixtures := map[string]Credentials{
		"wecom":    {WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test"},
		"lark":     {WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/12345678-abcd-abcd-abcd-123456789012"},
		"dingtalk": {WebhookURL: "https://oapi.dingtalk.com/robot/send?access_token=test"},
		"slack":    {WebhookURL: "https://hooks.slack.com/services/T123/B123/token"},
		"telegram": {BotToken: "123456:ABC_def-123", ChatID: "-100123456"},
	}
	for platform, credentials := range fixtures {
		t.Run(platform, func(t *testing.T) {
			// When
			_, err := ParseConfig(platform, credentials)
			// Then
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
