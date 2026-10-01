package communications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPPhoneClientDomesticContractAndIdempotency(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/calls" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("request=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["record"] != true || body["from"] != "+8613800138000" {
			t.Fatalf("body=%v", body)
		}
		_ = json.NewEncoder(w).Encode(Call{SID: "domestic-1", Status: "queued"})
	}))
	defer server.Close()
	client, err := NewHTTPPhoneClient(Config{HTTPBaseURL: server.URL, HTTPToken: "secret", FromNumber: "+8613800138000"}, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req := CallRequest{To: "+8613800138001", Message: "hello", Record: true, IdempotencyKey: "same"}
	one, err := client.StartCall(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	two, err := client.StartCall(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if one.SID != two.SID || calls != 1 {
		t.Fatalf("calls=%d one=%v two=%v", calls, one, two)
	}
}

func TestHTTPPhoneClientRetainsReservationAfterAmbiguousFailure(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "gateway timeout", http.StatusBadGateway)
	}))
	defer server.Close()
	client, err := NewHTTPPhoneClient(Config{HTTPBaseURL: server.URL, HTTPToken: "secret", FromNumber: "+8613800138000"}, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req := CallRequest{To: "+8613800138001", Message: "hello", IdempotencyKey: "ambiguous"}
	if _, err := client.StartCall(context.Background(), req); err == nil {
		t.Fatal("expected provider error")
	}
	if _, err := client.StartCall(context.Background(), req); !errors.Is(err, ErrAmbiguousOperation) {
		t.Fatalf("retry error=%v", err)
	}
	if calls != 1 {
		t.Fatalf("provider calls=%d, duplicate call was allowed", calls)
	}
}

func TestHTTPPhoneClientReleasesReservationAfterProviderRejection(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "invalid destination", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(Call{SID: "domestic-2", Status: "queued"})
	}))
	defer server.Close()
	client, err := NewHTTPPhoneClient(Config{HTTPBaseURL: server.URL, HTTPToken: "secret", FromNumber: "+8613800138000"}, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req := CallRequest{To: "+8613800138001", Message: "hello", IdempotencyKey: "releasable"}
	if _, err := client.StartCall(context.Background(), req); err == nil {
		t.Fatal("expected provider rejection")
	}
	call, err := client.StartCall(context.Background(), req)
	if err != nil || call.SID != "domestic-2" {
		t.Fatalf("retry call=%+v err=%v", call, err)
	}
	if calls != 2 {
		t.Fatalf("provider calls=%d", calls)
	}
}

func TestHTTPPhoneClientRejectsReusedKeyForDifferentPayload(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(Call{SID: "domestic-3", Status: "queued"})
	}))
	defer server.Close()
	client, err := NewHTTPPhoneClient(Config{HTTPBaseURL: server.URL, HTTPToken: "secret", FromNumber: "+8613800138000"}, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req := CallRequest{To: "+8613800138001", Message: "first", IdempotencyKey: "same-payload-key"}
	if _, err := client.StartCall(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Message = "second"
	if _, err := client.StartCall(context.Background(), req); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("error=%v", err)
	}
	if calls != 1 {
		t.Fatalf("provider calls=%d", calls)
	}
}

func TestHTTPPhoneClientValidatesReturnedCallAndTranscriptOwnership(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/calls/known":
			_ = json.NewEncoder(w).Encode(Call{SID: "other", Status: "queued"})
		case "/calls/known/cancel":
			_ = json.NewEncoder(w).Encode(Call{SID: "other", Status: "canceled"})
		case "/calls/known/transcriptions":
			_ = json.NewEncoder(w).Encode(map[string]any{"transcriptions": []Transcript{{SID: "tr-1", CallSID: "other"}}})
		case "/calls/empty-transcript/transcriptions":
			_ = json.NewEncoder(w).Encode(map[string]any{"transcriptions": []Transcript{{SID: "tr-2"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewHTTPPhoneClient(Config{HTTPBaseURL: server.URL, HTTPToken: "secret", FromNumber: "+8613800138000"}, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetCall(context.Background(), "known"); !errors.Is(err, ErrCallNotFound) {
		t.Fatalf("get ownership error=%v", err)
	}
	if _, err := client.CancelCall(context.Background(), "known"); !errors.Is(err, ErrCallNotFound) {
		t.Fatalf("cancel ownership error=%v", err)
	}
	if _, err := client.ListTranscriptions(context.Background(), "known"); !errors.Is(err, ErrCallNotFound) {
		t.Fatalf("transcript ownership error=%v", err)
	}
	transcripts, err := client.ListTranscriptions(context.Background(), "empty-transcript")
	if err != nil || len(transcripts) != 1 || transcripts[0].CallSID != "empty-transcript" {
		t.Fatalf("normalized transcripts=%+v err=%v", transcripts, err)
	}
}

func TestHTTPPhoneClientSendsConfiguredCallbacks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["status_callback"] != "https://example.test/status?sig=abc" || body["recording_status_callback"] != "https://example.test/recording" {
			t.Fatalf("callbacks=%v", body)
		}
		_ = json.NewEncoder(w).Encode(Call{SID: "callback-1", Status: "queued"})
	}))
	defer server.Close()
	client, err := NewHTTPPhoneClient(Config{
		HTTPBaseURL:                server.URL,
		HTTPToken:                  "secret",
		FromNumber:                 "+8613800138000",
		StatusCallbackURL:          "https://example.test/status?sig=abc",
		RecordingStatusCallbackURL: "https://example.test/recording",
	}, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.StartCall(context.Background(), CallRequest{To: "+8613800138001", Message: "hello", IdempotencyKey: "callback"}); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPPhoneClientRejectsUnsafeProviderURL(t *testing.T) {
	for _, cfg := range []Config{
		{HTTPBaseURL: "http://localhost:8080", HTTPToken: "secret", FromNumber: "+8613800138000"},
		{HTTPBaseURL: "https://user:pass@example.test", HTTPToken: "secret", FromNumber: "+8613800138000"},
		{HTTPBaseURL: "https://example.test?token=secret", HTTPToken: "secret", FromNumber: "+8613800138000"},
	} {
		if _, err := NewHTTPPhoneClient(cfg, nil, nil); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("unsafe URL %q accepted: %v", cfg.HTTPBaseURL, err)
		}
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	clientCfg := Config{HTTPBaseURL: server.URL, HTTPToken: "secret", FromNumber: "+8613800138000"}
	client, err := NewHTTPPhoneClient(clientCfg, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, callback := range []string{"http://example.test/callback", "https://user:pass@example.test/callback", "https://example.test/#fragment"} {
		if _, err := client.StartCall(context.Background(), CallRequest{To: "+8613800138001", Message: "hello", StatusCallback: callback, IdempotencyKey: callback}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("callback %q accepted: %v", callback, err)
		}
	}
}
