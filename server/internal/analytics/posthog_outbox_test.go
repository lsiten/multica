package analytics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostHogOutboxRetainsFailedBatchAcrossRestart(t *testing.T) {
	var offline atomic.Bool
	offline.Store(true)
	first := make(chan struct{}, 1)
	recovered := make(chan struct{}, 1)
	var mu sync.Mutex
	var original, replayed capturePayload
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body capturePayload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		if offline.Load() {
			original = body
			mu.Unlock()
			w.WriteHeader(503)
			select {
			case first <- struct{}{}:
			default:
			}
			return
		}
		replayed = body
		mu.Unlock()
		select {
		case recovered <- struct{}{}:
		default:
		}
	}))
	defer backend.Close()
	cfg := PostHogConfig{APIKey: "private-config-key", Host: backend.URL, QueueDir: t.TempDir(), BatchSize: 1, FlushEvery: time.Hour}
	client := NewPostHogClient(cfg)
	client.Capture(Event{Name: "must-survive", DistinctID: "fixture", Timestamp: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)})
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		client.Close()
		t.Fatal("first upload did not occur")
	}
	client.Close()
	entries, err := os.ReadDir(client.cfg.QueueDir)
	if err != nil {
		t.Fatal(err)
	}
	retained := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(client.cfg.QueueDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), cfg.APIKey) {
			t.Fatal("transport credential was persisted in the queue")
		}
		retained++
	}
	if retained != 1 {
		t.Fatalf("failed batches retained = %d", retained)
	}
	offline.Store(false)
	cfg.Now = func() time.Time { return time.Now().Add(time.Minute) }
	restarted := NewPostHogClient(cfg)
	select {
	case <-recovered:
	case <-time.After(2 * time.Second):
		restarted.Close()
		t.Fatal("saved batch not replayed after restart")
	}
	restarted.Close()
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(original, replayed) || len(original.Batch) != 1 || original.Batch[0].UUID == "" {
		t.Fatalf("retry changed event identity/timestamp/body: original=%+v replay=%+v", original, replayed)
	}
}

func TestPostHogClosePersistsWithoutWaitingForRemoteDelivery(t *testing.T) {
	started := make(chan struct{}, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer backend.Close()
	client := NewPostHogClient(PostHogConfig{APIKey: "fixture", Host: backend.URL, QueueDir: t.TempDir(), BatchSize: 1, FlushEvery: time.Hour})
	client.Capture(Event{Name: "first", DistinctID: "fixture"})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		client.Close()
		t.Fatal("upload never started")
	}
	for range 8 {
		client.Capture(Event{Name: "later", DistinctID: "fixture"})
	}
	closed := make(chan struct{})
	go func() { client.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("close waited for remote log delivery")
	}
	entries, err := os.ReadDir(client.cfg.QueueDir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			count++
		}
	}
	if count != 9 {
		t.Fatalf("accepted events lost while remote upload stalled: batches=%d want9", count)
	}
}

func TestPostHogOutboxCorruptRecordDoesNotBlockHealthyBatch(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{}`) }))
	defer backend.Close()
	client := NewPostHogClient(PostHogConfig{APIKey: "fixture", Host: backend.URL, QueueDir: t.TempDir()})
	client.Close()
	if err := client.persistBatch([]Event{{ID: "fixture-id", Name: "healthy", DistinctID: "fixture", Timestamp: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(client.cfg.QueueDir, "000-broken.json"), []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if pending, _ := client.replayBatches(context.Background()); pending {
		t.Fatal("corrupt record blocked valid batch")
	}
	if raw, err := os.ReadFile(filepath.Join(client.cfg.QueueDir, "failed", "000-broken.json")); err != nil || string(raw) != "{" {
		t.Fatalf("invalid batch evidence lost: %q %v", raw, err)
	}
}

func TestPostHogRetryDelayHonorsRateLimit(t *testing.T) {
	if delay := analyticsRetryDelay(1, &batchDeliveryError{status: 429, retryAfter: "120"}); delay < 2*time.Minute {
		t.Fatalf("retry ignored Retry-After: %s", delay)
	}
}
