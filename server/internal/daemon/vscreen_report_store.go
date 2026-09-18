package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const vscreenReportFileLimit = 1024 * 1024

func validQueuedVscreenReport(report protocol.VscreenIntervention) bool {
	if report.Validate() != nil || report.HumanSummary != "" || report.ContinuationTaskID != "" {
		return false
	}
	switch report.State {
	case protocol.VscreenInterventionAwaitingTakeover, protocol.VscreenInterventionHuman:
		return report.ReturnReceiptID == ""
	case protocol.VscreenInterventionReadyToContinue, protocol.VscreenInterventionCancelled, protocol.VscreenInterventionStale:
		return true
	default:
		return false
	}
}

func nextVscreenReport(previous, next protocol.VscreenIntervention) bool {
	if previous.WorkspaceID != next.WorkspaceID || previous.RuntimeID != next.RuntimeID || previous.AgentID != next.AgentID || previous.SourceTaskID != next.SourceTaskID || previous.Reason != next.Reason || previous.Epoch.NativeEpoch != next.Epoch.NativeEpoch || previous.Epoch.DisplayGeneration != next.Epoch.DisplayGeneration || previous.Epoch.GeometryRevision > next.Epoch.GeometryRevision {
		return false
	}
	switch previous.State {
	case protocol.VscreenInterventionAwaitingTakeover:
		return next.State == protocol.VscreenInterventionHuman || next.State == protocol.VscreenInterventionCancelled || next.State == protocol.VscreenInterventionStale
	case protocol.VscreenInterventionHuman:
		return next.State == protocol.VscreenInterventionReadyToContinue || next.State == protocol.VscreenInterventionCancelled || next.State == protocol.VscreenInterventionStale
	case protocol.VscreenInterventionReadyToContinue:
		return next.State == protocol.VscreenInterventionCancelled || next.State == protocol.VscreenInterventionStale
	default:
		return false
	}
}

func loadVscreenReportDisk(config vscreenReportConfig) (vscreenReportDisk, error) {
	disk := vscreenReportDisk{Format: 1, BackendIdentity: config.BackendIdentity, AccountID: config.AccountID, Entries: []vscreenReportEntry{}}
	backend, err := url.Parse(config.BackendIdentity)
	if err != nil || backend.Host == "" || (backend.Scheme != "http" && backend.Scheme != "https") || backend.User != nil || backend.RawQuery != "" || backend.Fragment != "" || config.AccountID == "" || len(config.AccountID) > 512 || strings.ContainsAny(config.AccountID, "\x00\r\n") || !filepath.IsAbs(config.Path) {
		return disk, vscreenReportError("invalid_configuration")
	}
	dir := filepath.Dir(config.Path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return disk, vscreenReportError("report_store_failed")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return disk, vscreenReportError("report_store_not_private")
	}
	info, err = os.Lstat(config.Path)
	if os.IsNotExist(err) {
		return disk, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return disk, vscreenReportError("report_store_not_private")
	}
	if info.Size() > vscreenReportFileLimit {
		return disk, vscreenReportError("report_store_invalid")
	}
	file, err := os.Open(config.Path)
	if err != nil {
		return disk, vscreenReportError("report_store_failed")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, vscreenReportFileLimit+1))
	if err != nil || len(raw) > vscreenReportFileLimit {
		return disk, vscreenReportError("report_store_invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&disk) != nil || decoder.Decode(new(json.RawMessage)) != io.EOF || disk.Format != 1 || len(disk.Entries) > vscreenReportLimit {
		return disk, vscreenReportError("report_store_invalid")
	}
	if disk.BackendIdentity != config.BackendIdentity || disk.AccountID != config.AccountID {
		return disk, vscreenReportError("report_scope_mismatch")
	}
	last := map[string]vscreenReportEntry{}
	requests := map[string]bool{}
	for _, entry := range disk.Entries {
		report := entry.Report
		if !validQueuedVscreenReport(report) || report.DaemonGeneration != "pending" || entry.Attempts < 0 || entry.Attempts > 16 || entry.Version < 0 || entry.Version > 0 && entry.Reason != "" || requests[report.RequestID] || entry.Reason != "" && !validVscreenReportFailure(entry.Reason) || entry.Attempts == 0 && (entry.Version > 0 || entry.Reason != "" || !entry.NextAttempt.IsZero()) || entry.NextAttempt.After(time.Now().Add(time.Minute)) {
			return disk, vscreenReportError("report_store_invalid")
		}
		if previous, ok := last[report.InterventionID]; ok {
			if !nextVscreenReport(previous.Report, report) || entry.Version > 0 && (previous.Version == 0 || entry.Version <= previous.Version) {
				return disk, vscreenReportError("report_store_invalid")
			}
		} else if report.State != protocol.VscreenInterventionAwaitingTakeover {
			return disk, vscreenReportError("report_store_invalid")
		}
		requests[report.RequestID] = true
		last[report.InterventionID] = entry
	}
	return disk, nil
}

func (r *vscreenReporter) persistLocked(entries []vscreenReportEntry) error {
	if r.failure != nil {
		return r.failure
	}
	disk := r.disk
	disk.Entries = entries
	raw, err := json.Marshal(disk)
	if err != nil || len(raw) > vscreenReportFileLimit {
		return vscreenReportError("report_capacity")
	}
	if err = writeVscreenReportFile(r.config.Path, raw); err != nil {
		r.failure = vscreenReportError("report_store_failed")
		r.current.Store(0)
		r.cancelFlightLocked()
		return r.failure
	}
	r.disk = disk
	return nil
}

func writeVscreenReportFile(path string, raw []byte) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".intervention-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
