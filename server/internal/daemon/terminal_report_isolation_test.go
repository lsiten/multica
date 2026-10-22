package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestTerminalReportReplayIsolatesNewTasksFromFailedReports(t *testing.T) {
	for _, signal := range []bool{false, true} {
		t.Run(map[bool]string{false: "worker process disk enqueue", true: "local wakeup"}[signal], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				d := New(Config{ServerBaseURL: "https://api.test", WorkspacesRoot: t.TempDir(), DaemonID: "isolated-reports"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
				var failedAttempts, delivered atomic.Int32
				d.terminalReportSend = func(_ context.Context, report terminalTaskReport, _ []time.Duration) error {
					if report.taskID == "failed-agent-task" {
						failedAttempts.Add(1)
						return errors.New("unavailable report endpoint")
					}
					delivered.Add(1)
					return nil
				}
				if err := d.terminalReports.enqueue(terminalTaskReport{kind: terminalTaskReportComplete, taskID: "failed-agent-task", output: "retained result"}); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan struct{})
				go func() { defer close(done); d.terminalReportReplayLoop(ctx) }()
				synctest.Wait()
				time.Sleep(5 * time.Second)
				synctest.Wait()
				if err := d.terminalReports.enqueue(terminalTaskReport{kind: terminalTaskReportComplete, taskID: "healthy-agent-task", output: "independent result"}); err != nil {
					t.Fatal(err)
				}
				if signal {
					d.signalTerminalReportReplay()
				}
				time.Sleep(2 * time.Second)
				synctest.Wait()
				if delivered.Load() != 1 || failedAttempts.Load() != 2 {
					t.Errorf("failed report delayed another agent or retried too often: delivered=%d, failed attempts=%d", delivered.Load(), failedAttempts.Load())
				}
				cancel()
				<-done
			})
		})
	}
}
