package daemon

import (
	"context"
	"sync"
	"time"
)

// replayPendingTerminalReports makes one delivery attempt per eligible report.
// A small fixed worker pool avoids one dead endpoint blocking every later task
// for the HTTP client's full timeout while still bounding reconnect pressure.
// Failed payloads back off independently; each eligible item gets one HTTP
// attempt. Failed quarantine compensation is queued for its own replay rather
// than holding the uploader through a second network retry schedule.
func (d *Daemon) replayPendingTerminalReports(ctx context.Context, retry *reportRetrySchedule) (pending, delivered int) {
	defer func() {
		waiting, settled := d.replayPendingWorktreeDeliveries(ctx, retry)
		pending += waiting
		delivered += settled
	}()
	if d.terminalReports == nil {
		return 0, 0
	}
	items, err := d.terminalReports.list()
	if err == nil {
		keys := make(map[string]struct{}, len(items))
		for _, item := range items {
			keys["terminal/"+item.fileName] = struct{}{}
		}
		retry.retain("terminal/", keys)
	}
	listFailed := err != nil
	if err != nil {
		d.logger.Error("load pending terminal reports", "error", err)
	}
	if len(items) == 0 {
		if listFailed {
			// Keep the replay timer alive so corrupt/unsupported/unreadable
			// records continue to alert instead of warning once at startup and
			// silently disappearing from operational view.
			return 1, 0
		}
		return 0, 0
	}

	workers := terminalReportReplayWorkers
	if len(items) < workers {
		workers = len(items)
	}
	jobs := make(chan pendingTerminalTaskReport)
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	remaining := len(items)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					continue
				}
				key := "terminal/" + item.fileName
				payload, encodeErr := terminalReportPayload(item.report)
				if encodeErr != nil {
					d.logger.Error("encode pending terminal report", "task", item.report.taskID, "error", encodeErr)
					continue
				}
				if !retry.ready(key, payload) {
					continue
				}
				release, ok := d.beginTerminalReportDelivery(item.report.storageKey())
				if !ok {
					continue
				}
				err := d.sendTerminalTaskReport(ctx, item.report, nil)
				quarantined := false
				if err == nil {
					err = d.terminalReports.acknowledge(item)
				} else {
					quarantined = d.handleTerminalReportDeliveryError(ctx, item, err)
				}
				release()
				retry.record(key, payload, err != nil && !quarantined)
				resultMu.Lock()
				if err == nil {
					remaining--
					delivered++
				} else if quarantined {
					remaining--
				} else {
					d.logger.Warn("pending terminal report remains queued",
						"task", item.report.taskID,
						"kind", item.report.kind,
						"error", err,
					)
				}
				resultMu.Unlock()
			}
		}()
	}
	for _, item := range items {
		select {
		case jobs <- item:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return remaining, delivered
		}
	}
	close(jobs)
	wg.Wait()
	if listFailed && remaining == 0 {
		remaining = 1
	}
	return remaining, delivered
}

func (d *Daemon) terminalReportReplayLoop(ctx context.Context) {
	// Another process owns this namespace, so control must not upload or it
	// would double-deliver the same terminal report. The owner (a gateway in
	// the split daemon) is the sole uploader; this process defers instead.
	if d.reportOutboxBlocked {
		d.logger.Info("report outbox is owned by another process; deferring terminal report uploads")
		return
	}
	if namespaces, err := d.terminalReports.otherNamespaceStats(); err != nil {
		d.logger.Warn("scan terminal report namespaces", "error", err)
	} else {
		for _, namespace := range namespaces {
			d.logger.Warn("terminal reports exist for a different daemon identity; not replaying",
				"namespace", namespace.name,
				"pending_count", namespace.stats.PendingCount,
				"pending_bytes", namespace.stats.PendingBytes,
				"failed_count", namespace.stats.FailedCount,
				"failed_bytes", namespace.stats.FailedBytes,
			)
		}
	}
	retry := &reportRetrySchedule{entries: make(map[string]reportRetryState), now: d.reportClock}
	var timer *time.Timer
	resetTimer := func(delay time.Duration) <-chan time.Time {
		if timer == nil {
			timer = time.NewTimer(delay)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(delay)
		}
		return timer.C
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	// Startup is itself a replay trigger. The queue is loaded only after auth
	// preflight, so recovered reports use the daemon's current credential.
	timerCh := resetTimer(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.terminalReportWakeup:
			timerCh = resetTimer(0)
		case <-timerCh:
			pending, delivered := d.replayPendingTerminalReports(ctx, retry)
			if delivered > 0 {
				d.logger.Info("replayed pending terminal reports", "delivered", delivered, "remaining", pending)
			}
			// Extracted workers share disk, not this wake channel. Discover new
			// records promptly even while unrelated payloads are backing off.
			timerCh = resetTimer(time.Second)
		}
	}
}
