package daemon

import (
	"encoding/json"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type vscreenReportNotice struct {
	report protocol.VscreenIntervention
	reason string
}

func (r *vscreenReporter) run() {
	defer close(r.done)
	r.mu.Lock()
	restored := append([]vscreenReportEntry(nil), r.disk.Entries...)
	r.mu.Unlock()
	for _, entry := range restored {
		if entry.Reason != "" {
			r.notify(&vscreenReportNotice{entry.Report, entry.Reason})
		}
	}
	for {
		notice, delay := r.step()
		r.notify(notice)
		var timer *time.Timer
		var tick <-chan time.Time
		if delay >= 0 {
			timer = time.NewTimer(delay)
			tick = timer.C
		}
		select {
		case <-r.stop:
			if timer != nil {
				timer.Stop()
			}
			return
		case ack := <-r.acks:
			if timer != nil {
				timer.Stop()
			}
			r.notify(r.acceptAck(ack))
		case <-r.wake:
			if timer != nil {
				timer.Stop()
			}
		case <-tick:
		}
	}
}

func (r *vscreenReporter) notify(notice *vscreenReportNotice) {
	if notice != nil && r.config.OnResult != nil {
		r.config.OnResult(notice.report, notice.reason)
	}
}

func (r *vscreenReporter) step() (*vscreenReportNotice, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.failure != nil || r.connection.binding == 0 {
		return nil, -1
	}
	now := time.Now()
	if r.flight != nil {
		if now.Before(r.flight.deadline) {
			return nil, time.Until(r.flight.deadline)
		}
		requestID := r.flight.envelope.RequestID
		r.cancelFlightLocked()
		for i, entry := range r.disk.Entries {
			if entry.Report.RequestID == requestID {
				return r.rejectLocked(i, "ack_timeout"), 0
			}
		}
	}
	blocked := map[string]bool{}
	delay := time.Duration(-1)
	for i, entry := range r.disk.Entries {
		if entry.Version > 0 {
			continue
		}
		id := entry.Report.InterventionID
		if blocked[id] {
			continue
		}
		blocked[id] = true
		if entry.Reason != "" {
			continue
		}
		if entry.Attempts >= r.config.MaxAttempts {
			return r.rejectLocked(i, "report_attempts_exhausted"), 0
		}
		if now.Before(entry.NextAttempt) {
			wait := time.Until(entry.NextAttempt)
			if delay < 0 || wait < delay {
				delay = wait
			}
			continue
		}
		entries := append([]vscreenReportEntry(nil), r.disk.Entries...)
		entries[i].Attempts++
		if err := r.persistLocked(entries); err != nil {
			return &vscreenReportNotice{entry.Report, "report_store_failed"}, -1
		}
		report := entry.Report
		report.DaemonGeneration = r.connection.generation
		payload, err := json.Marshal(report)
		if err != nil {
			return r.rejectLocked(i, "invalid_report"), 0
		}
		frame, err := json.Marshal(protocol.Message{Type: protocol.EventVscreenIntervention, Payload: payload})
		if err != nil {
			return r.rejectLocked(i, "invalid_report"), 0
		}
		outbound, err := r.connection.send(frame)
		if err != nil {
			return r.rejectLocked(i, "send_failed"), 0
		}
		r.flight = &vscreenReportFlight{envelope: report.VscreenEnvelope, interventionID: report.InterventionID, binding: r.connection.binding, deadline: time.Now().Add(r.config.AckTimeout), outbound: outbound}
		return nil, r.config.AckTimeout
	}
	return nil, delay
}

func (r *vscreenReporter) acceptAck(message vscreenReportAck) *vscreenReportNotice {
	r.mu.Lock()
	defer r.mu.Unlock()
	ack := message.ack
	if r.closed || r.failure != nil || r.flight == nil || message.binding != r.connection.binding || message.binding != r.flight.binding || ack.VscreenEnvelope != r.flight.envelope || ack.InterventionID != r.flight.interventionID {
		return nil
	}
	for i, entry := range r.disk.Entries {
		if entry.Report.RequestID != ack.RequestID {
			continue
		}
		if !ack.Accepted {
			r.cancelFlightLocked()
			return r.rejectLocked(i, ack.Reason)
		}
		for _, previous := range r.disk.Entries {
			if previous.Report.InterventionID == ack.InterventionID && previous.Version >= ack.Version {
				return nil
			}
		}
		entries := append([]vscreenReportEntry(nil), r.disk.Entries...)
		entries[i].Version = ack.Version
		entries[i].NextAttempt = time.Time{}
		if err := r.persistLocked(entries); err != nil {
			return &vscreenReportNotice{entry.Report, "report_store_failed"}
		}
		r.cancelFlightLocked()
		return &vscreenReportNotice{entry.Report, ""}
	}
	return nil
}

func (r *vscreenReporter) rejectLocked(index int, reason string) *vscreenReportNotice {
	entry := r.disk.Entries[index]
	entries := append([]vscreenReportEntry(nil), r.disk.Entries...)
	terminal := !retryableVscreenReportFailure(reason) || entry.Attempts >= r.config.MaxAttempts
	if terminal {
		entries[index].Reason = reason
	} else {
		shift := min(entry.Attempts-1, 4)
		if shift < 0 {
			shift = 0
		}
		entries[index].NextAttempt = time.Now().Add(min(r.config.RetryDelay*time.Duration(1<<shift), 30*time.Second))
	}
	if err := r.persistLocked(entries); err != nil {
		return &vscreenReportNotice{entry.Report, "report_store_failed"}
	}
	if terminal {
		return &vscreenReportNotice{entry.Report, reason}
	}
	return nil
}

func retryableVscreenReportFailure(reason string) bool {
	switch reason {
	case "source_not_stopped", "request_capacity", "daemon_timeout", "daemon_unavailable", "send_failed", "ack_timeout":
		return true
	default:
		return false
	}
}

func validVscreenReportFailure(reason string) bool {
	if retryableVscreenReportFailure(reason) {
		return true
	}
	switch reason {
	case "invalid_report", "permission_denied", "stale_generation", "not_found", "pending_conflict", "source_mismatch", "invalid_transition", "report_replayed", "intervention_failed", "report_attempts_exhausted":
		return true
	default:
		return false
	}
}
