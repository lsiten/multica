package daemon

import "github.com/multica-ai/multica/server/pkg/protocol"

// Consume removes one intervention only after the coordinator verifies a
// server-protected continuation claim. That claim proves server consumption
// even when the final report acknowledgement was lost on the previous socket.
func (r *vscreenReporter) Consume(workspaceID, runtimeID string, proof protocol.VscreenContinuationContext) error {
	if workspaceID == "" || runtimeID == "" || proof.InterventionID == "" || proof.SourceTaskID == "" || proof.ReturnReceiptID == "" || proof.Epoch.Validate() != nil {
		return vscreenReportError("invalid_scope")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return vscreenReportError("reporter_closed")
	}
	if r.failure != nil {
		return r.failure
	}
	found, matchedProof := false, false
	next := make([]vscreenReportEntry, 0, len(r.disk.Entries))
	for _, entry := range r.disk.Entries {
		report := entry.Report
		if report.InterventionID != proof.InterventionID {
			next = append(next, entry)
			continue
		}
		found = true
		if report.WorkspaceID != workspaceID || report.RuntimeID != runtimeID || report.SourceTaskID != proof.SourceTaskID || report.Epoch != proof.Epoch {
			return vscreenReportError("invalid_scope")
		}
		if report.State == protocol.VscreenInterventionReadyToContinue && report.ReturnReceiptID == proof.ReturnReceiptID {
			matchedProof = true
		}
	}
	if !found {
		return nil
	}
	if !matchedProof {
		return vscreenReportError("invalid_scope")
	}
	if err := r.persistLocked(next); err != nil {
		return err
	}
	if r.flight != nil && r.flight.interventionID == proof.InterventionID {
		r.cancelFlightLocked()
	}
	r.signal()
	return nil
}
