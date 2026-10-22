package daemon

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestQuarantineCompensationPersistsFailureAndKeepsExecutionFence(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: "https://owned.invalid", DaemonID: "fixture"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now()
	d.reportNow = func() time.Time { return now }
	identity := &protocol.ExecutionIdentity{TaskID: uuid.NewString(), RuntimeID: uuid.NewString(), ExecutionID: uuid.NewString(), WorkerID: uuid.NewString(), DispatchedAt: now}
	failCalls := 0
	acceptFailure := false
	d.terminalReportSend = func(_ context.Context, report terminalTaskReport, schedule []time.Duration) error {
		if len(schedule) != 0 {
			t.Error("background compensation used an inline network retry window")
		}
		if report.kind == terminalTaskReportComplete {
			return &requestError{StatusCode: 400}
		}
		failCalls++
		if report.execution == nil || report.execution.TaskID != identity.TaskID || report.execution.RuntimeID != identity.RuntimeID || report.execution.ExecutionID != identity.ExecutionID || report.execution.WorkerID != identity.WorkerID || !report.execution.DispatchedAt.Equal(identity.DispatchedAt) {
			t.Errorf("compensation lost exact worker execution identity got=%+v want=%+v", *report.execution, *identity)
		}
		if !acceptFailure {
			return &requestError{StatusCode: 503}
		}
		return nil
	}
	if err := d.reportTerminalTask(t.Context(), terminalTaskReport{kind: terminalTaskReportComplete, taskID: identity.TaskID, execution: identity, output: "real output"}); err != nil {
		t.Fatal(err)
	}
	for range terminalReportPermanentRejectionLimit {
		d.replayPendingTerminalReports(t.Context(), nil)
		now = now.Add(terminalReportPermanentRejectionAge)
	}
	items, err := d.terminalReports.list()
	if err != nil || len(items) != 1 || items[0].report.kind != terminalTaskReportFail || items[0].report.execution == nil {
		t.Fatalf("failed compensation was dropped: %+v %v", items, err)
	}
	if failCalls != 1 {
		t.Fatalf("compensation blocked or retried inline: %d", failCalls)
	}
	acceptFailure = true
	if pending, delivered := d.replayPendingTerminalReports(t.Context(), nil); pending != 0 || delivered != 1 {
		t.Fatalf("compensation retry failed: %d/%d", pending, delivered)
	}
	stats, err := d.terminalReports.stats()
	if err != nil || stats.FailedCount != 1 {
		t.Fatal("successful provider output was not retained after compensation")
	}
}
