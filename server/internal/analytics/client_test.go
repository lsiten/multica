package analytics

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestPostHogClient_Batching(t *testing.T) {
	var (
		mu       sync.Mutex
		received [][]captureItem
	)
	receivedBatch := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/batch/" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var payload capturePayload
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if payload.APIKey != "test-key" {
			t.Errorf("api_key = %q, want test-key", payload.APIKey)
		}
		mu.Lock()
		received = append(received, payload.Batch)
		mu.Unlock()
		select {
		case receivedBatch <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewPostHogClient(PostHogConfig{
		APIKey:     "test-key",
		Host:       srv.URL,
		BatchSize:  2,
		FlushEvery: time.Hour, // irrelevant, we hit the size trigger
		QueueDir:   t.TempDir(),
	})

	c.Capture(Event{Name: "signup", DistinctID: "u1", WorkspaceID: "w1"})
	c.Capture(Event{Name: "workspace_created", DistinctID: "u1", WorkspaceID: "w1"})
	select {
	case <-receivedBatch:
	case <-time.After(2 * time.Second):
		c.Close()
		t.Fatal("batch was not uploaded")
	}
	c.Close() // drains

	mu.Lock()
	defer mu.Unlock()
	total := 0
	for _, b := range received {
		total += len(b)
	}
	if total != 2 {
		t.Fatalf("received %d events, want 2 (batches=%d)", total, len(received))
	}
	// Both events should carry workspace_id in properties.
	for _, batch := range received {
		for _, item := range batch {
			if item.Properties["workspace_id"] != "w1" {
				t.Errorf("missing workspace_id on event %s", item.Event)
			}
			if item.DistinctID != "u1" {
				t.Errorf("distinct_id = %q, want u1", item.DistinctID)
			}
		}
	}
}

func TestPostHogClient_DropsWhenFull(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	c := NewPostHogClient(PostHogConfig{APIKey: "test-key", Host: srv.URL, QueueSize: 2, BatchSize: 1, FlushEvery: time.Hour, QueueDir: t.TempDir()})
	c.outboxMu.Lock()
	locked := true
	defer func() {
		if locked {
			c.outboxMu.Unlock()
		}
		c.Close()
	}()
	c.Capture(Event{Name: "spam", DistinctID: "u"})
	// Hold local persistence, rather than network delivery, to exercise the bounded producer queue.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for len(c.ch) > 0 {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("worker did not consume the first event")
		}
	}
	for range 3 {
		c.Capture(Event{Name: "spam", DistinctID: "u"})
	}
	if c.dropped.Load() == 0 {
		t.Fatal("queue overflow was not counted")
	}
	c.outboxMu.Unlock()
	locked = false
}

func TestEmailDomain(t *testing.T) {
	cases := map[string]string{
		"a@example.com":      "example.com",
		"user@Company.co.uk": "company.co.uk",
		"":                   "",
		"no-at":              "",
		"trailing@":          "",
	}
	for in, want := range cases {
		if got := emailDomain(in); got != want {
			t.Errorf("emailDomain(%q) = %q, want %q", in, got, want)
		}
	}
}
