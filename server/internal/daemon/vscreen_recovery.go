package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// A disable remains gated until the authenticated server acknowledges retirement
// of its pending rows. The persisted receipt also fences an interrupted restart.
func (d *Daemon) retainVscreenCleanup(s *vscreenRuntime, receipt protocol.VscreenCommandReceipt) error {
	s.interventions.mu.Lock()
	defer s.interventions.mu.Unlock()
	if s.interventions.records == nil {
		s.interventions.records = map[string]*vscreenInterventionRecord{}
	}
	record := s.interventions.records[receipt.RuntimeID]
	if record == nil {
		record = &vscreenInterventionRecord{Report: protocol.VscreenIntervention{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: receipt.WorkspaceID, RuntimeID: receipt.RuntimeID}}}
		s.interventions.records[receipt.RuntimeID] = record
	}
	record.CleanupReceipt = &receipt
	return d.persistVscreenInterventionsLocked(s)
}
func (d *Daemon) handleVscreenCleanupAck(msg mirrorOfferMessage) {
	var receipt protocol.VscreenCommandReceipt
	if json.Unmarshal(msg.raw, &receipt) != nil || receipt.Validate() != nil || receipt.State != protocol.VscreenReceiptSucceeded || !d.vscreenEnvelopeCurrent(receipt.VscreenEnvelope, msg.controlGeneration) {
		return
	}
	s := d.vscreenRuntime()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interventions.mu.Lock()
	defer s.interventions.mu.Unlock()
	record := s.interventions.records[receipt.RuntimeID]
	if record == nil || record.CleanupReceipt == nil || *record.CleanupReceipt != receipt {
		return
	}
	key, err := d.vscreenResource(receipt.WorkspaceID, receipt.RuntimeID)
	if err != nil || s.enabled[key] {
		return
	}
	actor, err := s.manager.For(key)
	if err != nil || actor.Status().Ready {
		return
	}
	if err = d.retireVscreenInterventionLocked(s, receipt.WorkspaceID, receipt.RuntimeID); err != nil {
		d.logger.Warn("virtual screen cleanup acknowledgement could not be persisted")
	}
}

// Call only after actual native disposal (or its confirmed no-resource result).
func (d *Daemon) retireVscreenInterventionLocked(s *vscreenRuntime, workspaceID, runtimeID string) error {
	record := s.interventions.records[runtimeID]
	if record != nil && record.Report.WorkspaceID != workspaceID {
		return &vscreen.Error{Reason: protocol.VscreenStaleSnapshot}
	}
	d.vscreenMu.Lock()
	reporter := d.vscreenReporter
	d.vscreenMu.Unlock()
	if reporter != nil {
		if err := reporter.CancelScope(workspaceID, runtimeID); err != nil {
			return err
		}
	}
	if record == nil {
		return nil
	}
	delete(s.interventions.records, runtimeID)
	if err := d.persistVscreenInterventionsLocked(s); err != nil {
		if record != nil {
			s.interventions.records[runtimeID] = record
		}
		return err
	}
	return nil
}
func (d *Daemon) removeVscreenRuntime(ctx context.Context, s *vscreenRuntime, runtimeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.enabled {
		if key.RuntimeID != runtimeID {
			continue
		}
		actor, err := s.manager.For(key)
		if err != nil {
			return err
		}
		ownerTaskID := actor.Status().Lease.TaskID
		if err = actor.Dispose(ctx); err != nil {
			return err
		}
		if err = d.stopDisposedVscreenExecution(ctx, s, ownerTaskID); err != nil {
			return err
		}
		s.interventions.mu.Lock()
		err = d.retireVscreenInterventionLocked(s, key.WorkspaceID, runtimeID)
		s.interventions.mu.Unlock()
		if err != nil {
			return err
		}
		delete(s.enabled, key)
	}
	// A previously disabled runtime may still await its server receipt.
	s.interventions.mu.Lock()
	defer s.interventions.mu.Unlock()
	if record := s.interventions.records[runtimeID]; record != nil {
		key := record.Report.WorkspaceID
		if err := d.retireVscreenInterventionLocked(s, key, runtimeID); err != nil {
			return err
		}
	}
	d.vscreenMu.Lock()
	reporter := d.vscreenReporter
	d.vscreenMu.Unlock()
	if reporter != nil {
		if err := reporter.CancelScope("", runtimeID); err != nil {
			return err
		}
	}
	return d.saveVscreenPreferences(s)
}

// Join the former owner's tool lifecycle before a successful disable can be
// acknowledged. Late cancellation callbacks must not freeze a replacement scene.
func (d *Daemon) stopDisposedVscreenExecution(ctx context.Context, s *vscreenRuntime, taskID string) error {
	if taskID == "" {
		return nil
	}
	s.interventions.mu.Lock()
	execution := s.interventions.executions[taskID]
	s.interventions.mu.Unlock()
	if execution == nil {
		return nil
	}
	execution.cancel()
	if execution.stopProvider != nil {
		execution.stopProvider(context.Canceled)
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case <-execution.done:
		return nil
	case <-wait.Done():
		return wait.Err()
	}
}
