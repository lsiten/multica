package communications

import (
	"context"
	"os"
	"strings"
	"time"
)

type CallProvider interface {
	StartCall(context.Context, CallRequest) (Call, error)
	GetCall(context.Context, string) (Call, error)
	CancelCall(context.Context, string) (Call, error)
	ListTranscriptions(context.Context, string) ([]Transcript, error)
}

type CallRequest struct {
	To             string
	Message        string
	Twiml          string
	StatusCallback string
	Record         bool
	IdempotencyKey string
}

type Call struct {
	SID         string `json:"sid"`
	Status      string `json:"status"`
	To          string `json:"to"`
	From        string `json:"from"`
	DateCreated string `json:"date_created"`
}

type Transcript struct {
	RecordingSID string `json:"recording_sid"`
	SID          string `json:"sid"`
	CallSID      string `json:"call_sid"`
	Status       string `json:"status"`
	Text         string `json:"transcription_text"`
	Language     string `json:"language_code"`
}

type Config struct {
	Provider                   string
	AccountSID                 string
	AuthToken                  string
	FromNumber                 string
	BaseURL                    string
	StatusCallbackURL          string
	RecordingStatusCallbackURL string
	HTTPBaseURL                string
	HTTPToken                  string
	Timeout                    time.Duration
}

func ConfigFromEnv() Config {
	return ConfigFromEnvValues(map[string]string{
		"TWILIO_ACCOUNT_SID":                   os.Getenv("TWILIO_ACCOUNT_SID"),
		"TWILIO_AUTH_TOKEN":                    os.Getenv("TWILIO_AUTH_TOKEN"),
		"TWILIO_FROM_NUMBER":                   os.Getenv("TWILIO_FROM_NUMBER"),
		"TWILIO_BASE_URL":                      os.Getenv("TWILIO_BASE_URL"),
		"TWILIO_STATUS_CALLBACK_URL":           os.Getenv("TWILIO_STATUS_CALLBACK_URL"),
		"TWILIO_RECORDING_STATUS_CALLBACK_URL": os.Getenv("TWILIO_RECORDING_STATUS_CALLBACK_URL"),
		"PHONE_PROVIDER":                       os.Getenv("PHONE_PROVIDER"), "PHONE_HTTP_BASE_URL": os.Getenv("PHONE_HTTP_BASE_URL"), "PHONE_HTTP_TOKEN": os.Getenv("PHONE_HTTP_TOKEN"),
		"PHONE_FROM_NUMBER":      os.Getenv("PHONE_FROM_NUMBER"),
		"TWILIO_TIMEOUT_SECONDS": os.Getenv("TWILIO_TIMEOUT_SECONDS"),
	})
}

// ConfigFromEnvValues builds configuration from task/agent custom environment
// values, keeping daemon-owned credentials outside the host process environment.
func ConfigFromEnvValues(values map[string]string) Config {
	timeout := 30 * time.Second
	if raw := strings.TrimSpace(values["TWILIO_TIMEOUT_SECONDS"]); raw != "" {
		if seconds, err := time.ParseDuration(raw + "s"); err == nil && seconds > 0 {
			timeout = seconds
		}
	}
	base := strings.TrimRight(strings.TrimSpace(values["TWILIO_BASE_URL"]), "/")
	if base == "" {
		base = "https://api.twilio.com"
	}
	cfg := Config{
		Provider:   strings.ToLower(strings.TrimSpace(values["PHONE_PROVIDER"])),
		AccountSID: strings.TrimSpace(values["TWILIO_ACCOUNT_SID"]),
		AuthToken:  strings.TrimSpace(values["TWILIO_AUTH_TOKEN"]),
		FromNumber: strings.TrimSpace(values["PHONE_FROM_NUMBER"]),
		BaseURL:    base, StatusCallbackURL: strings.TrimSpace(values["TWILIO_STATUS_CALLBACK_URL"]), RecordingStatusCallbackURL: strings.TrimSpace(values["TWILIO_RECORDING_STATUS_CALLBACK_URL"]), HTTPBaseURL: strings.TrimSpace(values["PHONE_HTTP_BASE_URL"]), HTTPToken: strings.TrimSpace(values["PHONE_HTTP_TOKEN"]),
		Timeout: timeout,
	}
	if cfg.FromNumber == "" {
		cfg.FromNumber = strings.TrimSpace(values["TWILIO_FROM_NUMBER"])
	}
	return cfg
}
