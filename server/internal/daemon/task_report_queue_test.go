package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestTaskDiagnosticsPersistWithoutWaitingForBackendAndReplayAfterRestart(t *testing.T) {
	var calls atomic.Int32
	var offline atomic.Bool
	offline.Store(true)
	var messages []TaskMessageData
	var usage []TaskUsageEntry
	var mu sync.Mutex
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if offline.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var body struct {
			Messages []TaskMessageData `json:"messages"`
			Usage    []TaskUsageEntry  `json:"usage"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		messages = append(messages, body.Messages...)
		usage = append(usage, body.Usage...)
		mu.Unlock()
	}))
	defer backend.Close()
	cfg := Config{ServerBaseURL: backend.URL, WorkspacesRoot: t.TempDir(), DaemonID: "fixture"}
	d := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rows := []TaskMessageData{{Seq: 1, Type: "text", Content: "must survive"}}
	tokens := []TaskUsageEntry{{Provider: "fixture", Model: "model", InputTokens: 123}}
	if err := d.client.ReportTaskMessages(context.Background(), "task", rows); err != nil {
		t.Fatal(err)
	}
	if err := d.client.ReportTaskUsage(context.Background(), "task", tokens); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("diagnostic enqueue waited for remote service")
	}
	if pending, delivered := d.replayPendingTerminalReports(t.Context()); pending != 2 || delivered != 0 {
		t.Fatalf("offline pending/delivered = %d/%d", pending, delivered)
	}
	offline.Store(false)
	restarted := New(cfg, d.logger)
	if pending, delivered := restarted.replayPendingTerminalReports(t.Context()); pending != 0 || delivered != 2 {
		t.Fatalf("recovery pending/delivered = %d/%d", pending, delivered)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 1 || messages[0].ID == "" || messages[0].Content != rows[0].Content || !reflect.DeepEqual(usage, tokens) {
		t.Fatalf("diagnostics lost: messages=%+v usage=%+v", messages, usage)
	}
	if pending, _ := restarted.replayPendingTerminalReports(t.Context()); pending != 0 {
		t.Fatal("acknowledged diagnostic retained")
	}
}

func TestTaskMessageEnqueueReplaysIdenticalIdsAndIsAccountScoped(t *testing.T) {
	cfg := Config{ServerBaseURL: "https://fixture.invalid", WorkspacesRoot: t.TempDir(), DaemonID: "fixture"}
	d := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.accountID = "first-account"
	d.terminalReports.bindAccount(d.accountID)
	rows := []TaskMessageData{{Seq: 1, Content: "original"}}
	for range 2 {
		if err := d.client.ReportTaskMessages(t.Context(), "task", rows); err != nil {
			t.Fatal(err)
		}
	}
	items, err := d.terminalReports.list()
	if err != nil || len(items) != 1 || items[0].report.messages[0].ID == "" {
		t.Fatalf("identical batch was not idempotent: %d %v", len(items), err)
	}
	d.terminalReports.bindAccount("second-account")
	if items, err := d.terminalReports.list(); err != nil || len(items) != 0 {
		t.Fatalf("another account adopted reports: %d %v", len(items), err)
	}
}
