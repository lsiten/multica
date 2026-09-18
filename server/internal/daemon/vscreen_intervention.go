package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type vscreenInterventionRecord struct {
	Report    protocol.VscreenIntervention `json:"report"`
	Stopped   bool                         `json:"stopped"`
	Windows   []string                     `json:"windows"`
	Authority appcontrol.Authority         `json:"-"`
}
type vscreenInterventions struct {
	mu         sync.Mutex
	records    map[string]*vscreenInterventionRecord
	executions map[string]*vscreenExecution
	windows    map[string]map[string]bool
}

func (d *Daemon) beginVscreenIntervention(task Task, actor *vscreen.Actor, reason error) error {
	s := d.vscreenRuntime()
	s.interventions.mu.Lock()
	defer s.interventions.mu.Unlock()
	if s.interventions.records == nil {
		s.interventions.records = map[string]*vscreenInterventionRecord{}
	}
	if existing := s.interventions.records[task.RuntimeID]; existing != nil && existing.Report.State != protocol.VscreenInterventionContinued {
		if existing.Report.SourceTaskID != task.ID {
			return errors.New("runtime intervention belongs to another task")
		}
		return nil
	}
	report := protocol.VscreenIntervention{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: task.WorkspaceID, RuntimeID: task.RuntimeID, RequestID: uuid.NewString()}, InterventionID: uuid.NewString(), AgentID: task.AgentID, SourceTaskID: task.ID, Reason: vscreenReason(reason), State: protocol.VscreenInterventionAwaitingTakeover, Epoch: actor.Status().Display.Epoch}
	lease := actor.Status().Lease
	windows := []string{}
	for window := range s.interventions.windows[task.ID] {
		windows = append(windows, window)
	}
	s.interventions.records[task.RuntimeID] = &vscreenInterventionRecord{Report: report, Windows: windows, Authority: appcontrol.Authority{Resource: lease.Resource, Epoch: report.Epoch, TaskID: task.ID, TransactionID: lease.TransactionID, LeaseEpoch: lease.LeaseEpoch}}
	return d.persistVscreenInterventionsLocked(s)
}
func (d *Daemon) persistVscreenInterventionsLocked(s *vscreenRuntime) error {
	if d.cfg.NativeVscreenPreferencesPath == "" {
		return errors.New("virtual screen durable state path unavailable")
	}
	filename := d.cfg.NativeVscreenPreferencesPath + ".intervention-state"
	raw, err := json.Marshal(s.interventions.records)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(filename), ".vscreen-intervention-*")
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
	return os.Rename(file.Name(), filename)
}
func (d *Daemon) loadVscreenInterventions(s *vscreenRuntime) {
	if d.cfg.NativeVscreenPreferencesPath == "" {
		return
	}
	raw, err := os.ReadFile(d.cfg.NativeVscreenPreferencesPath + ".intervention-state")
	if err != nil || len(raw) > 1<<20 {
		return
	}
	var records map[string]*vscreenInterventionRecord
	if json.Unmarshal(raw, &records) != nil {
		return
	}
	for runtimeID, record := range records {
		if record == nil || record.Report.RuntimeID != runtimeID {
			delete(records, runtimeID)
			continue
		}
		// Process and GUI authority are never restored from disk. A new native epoch cannot use old proof.
		record.Report.State = protocol.VscreenInterventionStale
		record.Report.ReturnReceiptID = ""
	}
	s.interventions.records = records
}
func (d *Daemon) markVscreenInterventionStopped(ctx context.Context, taskID string) error {
	s := d.vscreenRuntime()
	s.interventions.mu.Lock()
	var report *protocol.VscreenIntervention
	for _, record := range s.interventions.records {
		if record.Report.SourceTaskID == taskID && record.Report.State == protocol.VscreenInterventionAwaitingTakeover {
			record.Stopped = true
			copy := record.Report
			report = &copy
			break
		}
	}
	err := d.persistVscreenInterventionsLocked(s)
	s.interventions.mu.Unlock()
	if err != nil {
		return err
	}
	if report == nil {
		return nil
	}
	return d.enqueueVscreenIntervention(ctx, *report)
}
func (d *Daemon) projectVscreenIntervention(s *vscreenRuntime, state *protocol.VscreenStateSnapshot) {
	s.interventions.mu.Lock()
	defer s.interventions.mu.Unlock()
	record := s.interventions.records[state.RuntimeID]
	if record == nil || record.Report.State == protocol.VscreenInterventionContinued {
		return
	}
	if record.Report.Epoch != (protocol.VscreenEpoch{NativeEpoch: state.NativeEpoch, DisplayGeneration: state.DisplayGeneration, GeometryRevision: state.GeometryRevision}) {
		state.ControlState = protocol.VscreenControlStopping
		state.ActiveTaskID = nil
		return
	}
	id := record.Report.InterventionID
	state.InterventionID = &id
	state.ActiveTaskID = nil
	if !record.Stopped {
		state.ControlState = protocol.VscreenControlStopping
		return
	}
	switch record.Report.State {
	case protocol.VscreenInterventionAwaitingTakeover:
		state.ControlState = protocol.VscreenControlAwaitingTakeover
	case protocol.VscreenInterventionHuman:
		state.ControlState = protocol.VscreenControlHuman
	case protocol.VscreenInterventionReadyToContinue:
		state.ControlState = protocol.VscreenControlAwaitingTakeover
	default:
		state.ControlState = protocol.VscreenControlStopping
		return
	}
	state.InterventionState = record.Report.State
	state.ReturnReceiptID = record.Report.ReturnReceiptID
}
func (d *Daemon) validateVscreenContinuation(ctx context.Context, s *vscreenRuntime, task Task, a *vscreen.Actor) error {
	s.interventions.mu.Lock()
	defer s.interventions.mu.Unlock()
	record := s.interventions.records[task.RuntimeID]
	continuation := task.VscreenContinuation
	if continuation == nil {
		if record != nil && record.Report.State != protocol.VscreenInterventionContinued {
			return a.Suspend(ctx)
		}
		return nil
	}
	if record == nil || record.Report.State != protocol.VscreenInterventionReadyToContinue || !record.Stopped || record.Report.SourceTaskID != continuation.SourceTaskID || record.Report.InterventionID != continuation.InterventionID || record.Report.ReturnReceiptID != continuation.ReturnReceiptID || record.Report.Epoch != continuation.Epoch || a.Status().Display.Epoch != continuation.Epoch || record.Report.AgentID != task.AgentID || record.Report.WorkspaceID != task.WorkspaceID {
		return errors.New("virtual screen continuation proof is not current")
	}
	if continuation.FreshSession && task.PriorSessionID != "" {
		return errors.New("fresh continuation cannot resume old session")
	}
	if err := a.Recover(ctx, continuation.Epoch); err != nil {
		return err
	}
	record.Report.State = protocol.VscreenInterventionContinued
	record.Report.ContinuationTaskID = task.ID
	return d.persistVscreenInterventionsLocked(s)
}
