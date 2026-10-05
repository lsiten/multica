package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorktreeDeliveryMetadataSurvivesTerminalOutbox(t *testing.T) {
	report := terminalTaskReport{kind: terminalTaskReportComplete, taskID: "task", output: "done", worktreeDeliveryPending: true}
	stored, err := persistedTerminalReport(report, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var decoded persistedTerminalTaskReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	resumed, err := decoded.terminalReport()
	if err != nil || !resumed.worktreeDeliveryPending {
		t.Fatalf("outbox lost pending delivery: %+v %v", resumed, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["worktree_delivery_pending"] != true {
			t.Errorf("pending field missing: %+v", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	d := &Daemon{client: NewClient(server.URL)}
	if err := d.sendTerminalTaskReport(context.Background(), resumed, nil); err != nil {
		t.Fatal(err)
	}
	report.worktreeDeliveryPending = false
	report.worktreeCommit = strings.Repeat("a", 40)
	stored, err = persistedTerminalReport(report, time.Now())
	if err != nil || stored.WorktreeCommit != report.worktreeCommit {
		t.Fatal("outbox lost final commit")
	}
}
