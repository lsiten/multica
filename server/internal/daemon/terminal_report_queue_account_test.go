package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestTerminalReportStoreAccountAwareAdoptsMatching(t *testing.T) {
	store := newTerminalReportStore(Config{
		WorkspacesRoot: t.TempDir(),
		ServerBaseURL:  "https://api.example.test",
		Profile:        "account",
		DaemonID:       "daemon-account",
	})
	store.bindAccount("account-a")

	report := terminalTaskReport{
		kind:      terminalTaskReportComplete,
		taskID:    "task-a",
		output:    "final answer",
		accountID: "account-a",
	}
	if err := store.enqueue(report); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	items, err := store.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].report.taskID != "task-a" {
		t.Fatalf("matching account not adopted: %+v", items)
	}
}

func TestTerminalReportStoreAccountAwareAdoptsMatchingEnqueuedBeforeBind(t *testing.T) {
	store := newTerminalReportStore(Config{
		WorkspacesRoot: t.TempDir(),
		ServerBaseURL:  "https://api.example.test",
		Profile:        "account",
		DaemonID:       "daemon-account",
	})
	// Enqueue before binding: the quarantine is decided by account match, not
	// by the order in which the account was bound.
	report := terminalTaskReport{
		kind:      terminalTaskReportComplete,
		taskID:    "task-m",
		accountID: "account-a",
	}
	if err := store.enqueue(report); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	store.bindAccount("account-a")

	items, err := store.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].report.taskID != "task-m" {
		t.Fatalf("matching account not adopted after bind: %+v", items)
	}
}

func TestTerminalReportStoreAccountAwareQuarantinesOtherAndLegacy(t *testing.T) {
	store := newTerminalReportStore(Config{
		WorkspacesRoot: t.TempDir(),
		ServerBaseURL:  "https://api.example.test",
		Profile:        "account",
		DaemonID:       "daemon-account",
	})

	// A legacy v1 record carries no account: it was written before an account
	// was resolved and must be preserved, never adopted under a new login.
	legacy := terminalTaskReport{
		kind:   terminalTaskReportComplete,
		taskID: "task-legacy",
	}
	if err := store.enqueue(legacy); err != nil {
		t.Fatalf("enqueue legacy: %v", err)
	}
	// Another account's record must never be replayed under this login.
	other := terminalTaskReport{
		kind:      terminalTaskReportComplete,
		taskID:    "task-other",
		accountID: "account-b",
	}
	if err := store.enqueue(other); err != nil {
		t.Fatalf("enqueue other: %v", err)
	}

	store.bindAccount("account-a")

	items, err := store.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("quarantined records were adopted: %+v", items)
	}

	// Quarantined records stay on disk (preserved, not deleted) so a later pass
	// under the owning account can still deliver them.
	stats, err := store.stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.PendingCount != 2 {
		t.Fatalf("pending count = %d, want 2 preserved records", stats.PendingCount)
	}
}

func TestTerminalReportStoreLegacyModeUploadsAll(t *testing.T) {
	store := newTerminalReportStore(Config{
		WorkspacesRoot: t.TempDir(),
		ServerBaseURL:  "https://api.example.test",
		Profile:        "account",
		DaemonID:       "daemon-account",
	})
	// No bindAccount: legacy behaviour uploads every record regardless of any
	// account it may carry.
	withAccount := terminalTaskReport{
		kind:      terminalTaskReportComplete,
		taskID:    "task-with",
		accountID: "account-a",
	}
	if err := store.enqueue(withAccount); err != nil {
		t.Fatalf("enqueue with account: %v", err)
	}
	legacy := terminalTaskReport{
		kind:   terminalTaskReportComplete,
		taskID: "task-legacy",
	}
	if err := store.enqueue(legacy); err != nil {
		t.Fatalf("enqueue legacy: %v", err)
	}

	items, err := store.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("legacy mode did not upload all records: %+v", items)
	}
}

func TestTerminalReportStoreBindAccountEmptyStaysLegacy(t *testing.T) {
	store := newTerminalReportStore(Config{
		WorkspacesRoot: t.TempDir(),
		ServerBaseURL:  "https://api.example.test",
		Profile:        "account",
		DaemonID:       "daemon-account",
	})
	// An empty account must not switch the store into account-aware mode.
	store.bindAccount("")

	report := terminalTaskReport{
		kind:   terminalTaskReportComplete,
		taskID: "task-legacy",
	}
	if err := store.enqueue(report); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if store.accountAware {
		t.Fatal("empty account must not enable account-aware mode")
	}
	items, err := store.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("empty-account store did not stay in legacy upload-all mode: %+v", items)
	}
}

// TestDaemonReportTerminalTaskStampsAccount exercises the production path: the
// daemon resolves its account, binds the store, and a completed task must land
// in the outbox carrying that account so the account-aware replay pass adopts
// it (and a later login change would quarantine it instead).
func TestDaemonReportTerminalTaskStampsAccount(t *testing.T) {
	cfg := Config{
		ServerBaseURL:  "https://api.example.test",
		WorkspacesRoot: t.TempDir(),
		Profile:        "account-stamp",
		DaemonID:       "daemon-account-stamp",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := New(cfg, logger)
	d.accountID = "account-a"
	d.terminalReports.bindAccount("account-a")
	// The online callback fails so the record stays pending and inspectable;
	// a non-permanent error is not quarantined, only retained for replay.
	d.terminalReportSend = func(context.Context, terminalTaskReport, []time.Duration) error {
		return errors.New("offline")
	}

	report := terminalTaskReport{
		kind:   terminalTaskReportComplete,
		taskID: "task-stamp",
		output: "final answer",
	}
	if err := d.reportTerminalTask(context.Background(), report); err == nil {
		t.Fatal("expected delivery to fail while offline")
	}

	items, err := d.terminalReports.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("account-stamped record not adopted: %+v", items)
	}
	if items[0].report.accountID != "account-a" {
		t.Fatalf("record account = %q, want account-a", items[0].report.accountID)
	}
}
