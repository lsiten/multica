package communications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testClient(t *testing.T, handler http.Handler) *TwilioClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client, err := NewTwilioClient(Config{AccountSID: "AC123", AuthToken: "secret", FromNumber: "+15550000000", BaseURL: srv.URL}, srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestTwilioStartCallUsesHostConfigAndIdempotency(t *testing.T) {
	calls := 0
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/2010-04-01/Accounts/AC123/Calls.json" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if user, pass, ok := r.BasicAuth(); !ok || user != "AC123" || pass != "secret" {
			t.Errorf("bad auth")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("To") != "+15551112222" || r.Form.Get("From") != "+15550000000" || r.Form.Get("StatusCallback") != "https://example.test/callback" {
			t.Errorf("form = %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sid": "CA1", "status": "queued", "to": "+15551112222", "from": "+15550000000"})
	}))
	req := CallRequest{To: "+15551112222", Twiml: "<Response><Say>Hello</Say></Response>", StatusCallback: "https://example.test/callback", IdempotencyKey: "task-1"}
	first, err := client.StartCall(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.StartCall(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.SID != second.SID || calls != 1 {
		t.Fatalf("call = %#v/%#v requests=%d", first, second, calls)
	}
}

func TestTwilioStartCallRequestsRecordingWhenEnabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("Record") != "true" || r.Form.Get("RecordingStatusCallback") != "https://example.test/recording" {
			t.Fatalf("recording form = %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sid": "CA-record", "status": "queued"})
	}))
	defer server.Close()
	client, err := NewTwilioClient(Config{AccountSID: "AC123", AuthToken: "secret", FromNumber: "+15550000000", BaseURL: server.URL, RecordingStatusCallbackURL: "https://example.test/recording"}, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.StartCall(context.Background(), CallRequest{To: "+15551112222", Twiml: "<Response/>", Record: true, IdempotencyKey: "record"}); err != nil {
		t.Fatal(err)
	}
}

func TestTwilioAmbiguousNetworkFailureBlocksDuplicate(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "", http.StatusGatewayTimeout) }))
	req := CallRequest{To: "+15551112222", Twiml: "<Response/>", IdempotencyKey: "task-ambiguous"}
	if _, err := client.StartCall(context.Background(), req); err == nil {
		t.Fatal("expected provider failure")
	}
	_, err := client.StartCall(context.Background(), req)
	if !errors.Is(err, ErrAmbiguousOperation) {
		t.Fatalf("retry error = %v", err)
	}
}

func TestTwilioClientOperationsAndValidation(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/Calls/CA1.json"):
			_ = json.NewEncoder(w).Encode(map[string]any{"sid": "CA1", "status": "in-progress"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/Calls/CA1.json"):
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("Status") != "completed" {
				t.Errorf("cancel form=%v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"sid": "CA1", "status": "canceled"})
		case strings.HasSuffix(r.URL.Path, "/Calls/CA1/Recordings.json"):
			_ = json.NewEncoder(w).Encode(map[string]any{"recordings": []map[string]string{{"sid": "RE1", "call_sid": "CA1"}}})
		case strings.HasSuffix(r.URL.Path, "/Recordings/RE1/Transcriptions.json"):
			_ = json.NewEncoder(w).Encode(map[string]any{"transcriptions": []map[string]string{{"sid": "TR1", "recording_sid": "RE1", "status": "completed", "transcription_text": "hello"}}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	call, err := client.GetCall(context.Background(), "CA1")
	if err != nil || call.Status != "in-progress" {
		t.Fatalf("get = %#v %v", call, err)
	}
	call, err = client.CancelCall(context.Background(), "CA1")
	if err != nil || call.Status != "canceled" {
		t.Fatalf("cancel = %#v %v", call, err)
	}
	transcripts, err := client.ListTranscriptions(context.Background(), "CA1")
	if err != nil || len(transcripts) != 1 || transcripts[0].Text != "hello" {
		t.Fatalf("transcripts = %#v %v", transcripts, err)
	}
	for _, req := range []CallRequest{{To: "123", Twiml: "<Response/>", IdempotencyKey: "a"}, {To: "+15551112222", Twiml: "", IdempotencyKey: "b"}, {To: "+15551112222", Twiml: "<Response/>", IdempotencyKey: ""}} {
		if _, err := client.StartCall(context.Background(), req); !errors.Is(err, ErrInvalidRequest) && !errors.Is(err, ErrInvalidPhone) {
			t.Fatalf("validation error=%v", err)
		}
	}
}

func TestTwilioConfigFromEnv(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "ACenv")
	t.Setenv("TWILIO_AUTH_TOKEN", "tok")
	t.Setenv("TWILIO_FROM_NUMBER", "+15550000000")
	t.Setenv("TWILIO_TIMEOUT_SECONDS", "7")
	cfg := ConfigFromEnv()
	if cfg.AccountSID != "ACenv" || cfg.Timeout.String() != "7s" {
		t.Fatalf("config=%+v", cfg)
	}
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		t.Fatal(err)
	}
	values := ConfigFromEnvValues(map[string]string{"TWILIO_ACCOUNT_SID": "ACtask", "TWILIO_AUTH_TOKEN": "task-token", "TWILIO_FROM_NUMBER": "+15550000001"})
	if values.AccountSID != "ACtask" || values.AuthToken != "task-token" || values.FromNumber != "+15550000001" {
		t.Fatalf("task config=%+v", values)
	}
}

func TestIMAPConfigAndReceiverValidation(t *testing.T) {
	cfg := IMAPConfigFromEnvValues(map[string]string{"IMAP_HOST": "imap.example.test", "IMAP_USERNAME": "agent@example.test", "IMAP_PASSWORD": "secret", "IMAP_PORT": "993", "IMAP_TIMEOUT_SECONDS": "5"})
	if cfg.Host != "imap.example.test" || cfg.Mailbox != "INBOX" || cfg.Timeout.String() != "5s" {
		t.Fatalf("imap config=%+v", cfg)
	}
	if _, err := NewIMAPReceiver(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := NewIMAPReceiver(IMAPConfig{}); !errors.Is(err, ErrIMAPNotConfigured) {
		t.Fatalf("validation error=%v", err)
	}
}

func TestTwilioRejectsReusingKeyForDifferentContent(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Call{SID: "CA1", Status: "queued"})
	}))
	request := CallRequest{To: "+15551112222", Twiml: "<Response/>", IdempotencyKey: "same"}
	if _, err := client.StartCall(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.To = "+15551112223"
	if _, err := client.StartCall(context.Background(), request); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("err=%v", err)
	}
}
func TestTwilioDoesNotFollowRedirectOrForwardSecrets(t *testing.T) {
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; w.WriteHeader(200) }))
	defer target.Close()
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	_, err := client.StartCall(context.Background(), CallRequest{To: "+15551112222", Twiml: "<Response/>", IdempotencyKey: "redirect"})
	if err == nil || leaked {
		t.Fatalf("err=%v leaked=%v", err, leaked)
	}
}

func TestTwilioRejectsInsecureConfiguredCallbacks(t *testing.T) {
	for _, callback := range []string{"http://example.test/callback", "https:///missing-host", "https://user:password@example.test"} {
		_, err := NewTwilioClient(Config{AccountSID: "AC123", AuthToken: "test", FromNumber: "+15550000000", BaseURL: "https://api.twilio.com", StatusCallbackURL: callback}, nil, nil)
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("callback %q accepted: %v", callback, err)
		}
	}
}
