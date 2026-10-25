package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTerminalReportQuarantinePreservesConflictingPayloads(t *testing.T) {
	for _, previous := range []string{"different payload", "corrupt record"} {
		t.Run(previous, func(t *testing.T) {
			d := New(Config{ServerBaseURL: "https://api.example.test", WorkspacesRoot: t.TempDir(), DaemonID: "conflicting-quarantine"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			base := time.Date(2026, time.October, 5, 0, 0, 0, 0, time.UTC)
			now := base
			d.reportNow = func() time.Time { return now }
			old := terminalTaskReport{kind: terminalTaskReportFail, taskID: "repeated-task", errorMessage: "earlier failure"}
			current := old
			current.errorMessage = "later failure"
			if err := d.terminalReports.enqueue(old); err != nil {
				t.Fatal(err)
			}
			if err := ensureTerminalReportDir(d.terminalReports.failedDir()); err != nil {
				t.Fatal(err)
			}
			name := terminalReportFileName(old.taskID)
			oldPath := filepath.Join(d.terminalReports.failedDir(), name)
			if err := os.Rename(filepath.Join(d.terminalReports.dir, name), oldPath); err != nil {
				t.Fatal(err)
			}
			if previous == "corrupt record" {
				if err := os.WriteFile(oldPath, []byte("unreadable preserved evidence"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			originalBytes, err := os.ReadFile(oldPath)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			d.terminalReportSend = func(context.Context, terminalTaskReport, []time.Duration) error {
				calls++
				return &requestError{StatusCode: http.StatusNotFound, Body: `{"error":"task not found"}`}
			}
			if err := d.reportTerminalTask(context.Background(), current); err != nil {
				t.Fatalf("local queue rejected valid report: %v", err)
			}
			d.replayPendingTerminalReports(t.Context(), nil)
			for _, age := range []time.Duration{5 * time.Minute, terminalReportPermanentRejectionAge} {
				now = base.Add(age)
				d.replayPendingTerminalReports(context.Background(), nil)
			}
			stats, err := d.terminalReports.stats()
			if err != nil || stats.PendingCount != 0 || stats.FailedCount != 2 {
				t.Fatalf("queue stats = %+v, error=%v; want 0 pending and 2 preserved failures", stats, err)
			}
			unchanged, err := os.ReadFile(oldPath)
			if err != nil || string(unchanged) != string(originalBytes) {
				t.Fatalf("earlier quarantined evidence changed: %v", err)
			}
			entries, err := os.ReadDir(d.terminalReports.failedDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() == name {
					continue
				}
				body, err := os.ReadFile(filepath.Join(d.terminalReports.failedDir(), entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				record, err := decodePersistedTerminalReport(body)
				if err != nil || record.ErrorMessage != current.errorMessage || record.QuarantinedAt == nil {
					t.Fatalf("later failure not durably quarantined: %+v %v", record, err)
				}
			}
			if err := d.reportTerminalTask(context.Background(), current); err != nil {
				t.Fatalf("local queue rejected repeated report: %v", err)
			}
			d.replayPendingTerminalReports(t.Context(), nil)
			for _, age := range []time.Duration{5 * time.Minute, terminalReportPermanentRejectionAge} {
				now = base.Add(terminalReportPermanentRejectionAge + age)
				d.replayPendingTerminalReports(context.Background(), nil)
			}
			stats, err = d.terminalReports.stats()
			if err != nil || stats.PendingCount != 0 || stats.FailedCount != 2 {
				t.Fatalf("repeated payload was not deduplicated: %+v %v", stats, err)
			}
			for range 3 {
				d.replayPendingTerminalReports(context.Background(), nil)
			}
			if calls != 2*terminalReportPermanentRejectionLimit {
				t.Fatalf("delivery attempts = %d, want %d and no post-quarantine replay", calls, 2*terminalReportPermanentRejectionLimit)
			}
		})
	}
}

func TestTerminalReportQuarantineMatchesDecodedVerification(t *testing.T) {
	d := New(Config{ServerBaseURL: "https://api.example.test", WorkspacesRoot: t.TempDir(), DaemonID: "verification-quarantine"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	base := time.Date(2026, time.October, 5, 0, 0, 0, 0, time.UTC)
	now := base
	d.reportNow = func() time.Time { return now }
	confidence := 0.9
	report := terminalTaskReport{
		kind: terminalTaskReportComplete, taskID: "verified-task", output: "verified result",
		jevVerification: &JevVerification{Verified: true, Confidence: &confidence, Probabilities: map[string]float64{"accept": 0.9}},
	}
	if err := d.terminalReports.enqueue(report); err != nil {
		t.Fatal(err)
	}
	if err := d.terminalReports.enqueue(report); err != nil {
		t.Fatalf("identical decoded payload rejected as a conflict: %v", err)
	}
	empty := report
	empty.taskID = "empty-verification-map"
	empty.jevVerification = &JevVerification{Verified: true, Probabilities: map[string]float64{}}
	if err := d.terminalReports.enqueue(empty); err != nil {
		t.Fatal(err)
	}
	if err := d.terminalReports.enqueue(empty); err != nil {
		t.Fatalf("omitted empty map changed payload identity: %v", err)
	}
	d.terminalReportSend = func(context.Context, terminalTaskReport, []time.Duration) error {
		return &requestError{StatusCode: http.StatusNotFound, Body: `{"error":"task not found"}`}
	}
	for _, age := range []time.Duration{0, 5 * time.Minute, terminalReportPermanentRejectionAge} {
		now = base.Add(age)
		d.replayPendingTerminalReports(context.Background(), nil)
	}
	stats, err := d.terminalReports.stats()
	if err != nil || stats.PendingCount != 0 || stats.FailedCount != 2 {
		t.Fatalf("verification report not quarantined: %+v %v", stats, err)
	}
}

func TestQuarantineCompensationOnConflict(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: "https://owned.invalid", DaemonID: "conflict-compensation"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now()
	d.reportNow = func() time.Time { return now }
	failCalls := 0
	d.terminalReportSend = func(_ context.Context, report terminalTaskReport, schedule []time.Duration) error {
		if len(schedule) != 0 {
			t.Error("background compensation used an inline network retry window")
		}
		if report.kind == terminalTaskReportComplete {
			// A 409 on /complete is an ErrExecutionStale fence: permanent and
			// unrecoverable for this payload, so it must be quarantined rather
			// than replayed forever.
			return &requestError{StatusCode: http.StatusConflict}
		}
		failCalls++
		// The Fail compensation is a distinct transient request; keep it pending
		// so the quarantined completion stays preserved in failed/.
		return &requestError{StatusCode: http.StatusServiceUnavailable}
	}
	if err := d.reportTerminalTask(t.Context(), terminalTaskReport{kind: terminalTaskReportComplete, taskID: "conflict-task", output: "real output"}); err != nil {
		t.Fatalf("local queue rejected valid report: %v", err)
	}
	for range terminalReportPermanentRejectionLimit {
		d.replayPendingTerminalReports(t.Context(), nil)
		now = now.Add(terminalReportPermanentRejectionAge)
	}
	if failCalls != 1 {
		t.Fatalf("conflict did not trigger the fail compensation exactly once: %d", failCalls)
	}
	stats, err := d.terminalReports.stats()
	if err != nil {
		t.Fatalf("queue stats: %v", err)
	}
	if stats.FailedCount != 1 {
		t.Fatalf("conflict-quarantined completion was not retained: %+v", stats)
	}
	// The fail compensation is the only surviving pending record; the original
	// completion is preserved in failed/ and never overwrites it.
	items, err := d.terminalReports.list()
	if err != nil || len(items) != 1 || items[0].report.kind != terminalTaskReportFail || items[0].report.taskID != "conflict-task" {
		t.Fatalf("fail compensation dropped or completion lost: %+v %v", items, err)
	}
}
