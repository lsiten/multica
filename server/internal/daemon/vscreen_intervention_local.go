package daemon

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// SetVscreenLocalOwnerVerifier installs the trusted Desktop handshake verifier.
// Server commands and remote viewer grants never pass through this boundary.
func (d *Daemon) SetVscreenLocalOwnerVerifier(verify func(context.Context, string) bool) {
	d.vscreenMu.Lock()
	defer d.vscreenMu.Unlock()
	d.vscreenLocalOwner = verify
}

// TakeOverVscreenLocally moves only registered windows after provider stop and native quiescence.
func (d *Daemon) TakeOverVscreenLocally(ctx context.Context, ownerCapability, workspaceID, runtimeID, interventionID, destination string) error {
	return d.transferVscreenLocally(ctx, ownerCapability, workspaceID, runtimeID, interventionID, destination, "to_real", "")
}

// ReturnVscreenLocally returns owned windows and obtains a fresh native observation before minting proof.
func (d *Daemon) ReturnVscreenLocally(ctx context.Context, ownerCapability, workspaceID, runtimeID, interventionID, summary string) error {
	return d.transferVscreenLocally(ctx, ownerCapability, workspaceID, runtimeID, interventionID, "", "to_virtual", summary)
}
func (d *Daemon) transferVscreenLocally(ctx context.Context, capability, workspaceID, runtimeID, id, destination, direction, summary string) error {
	if len(summary) > 2048 || !utf8.ValidString(summary) {
		return errors.New("invalid human summary")
	}
	d.vscreenMu.Lock()
	verify := d.vscreenLocalOwner
	reporter := d.vscreenReporter
	d.vscreenMu.Unlock()
	if verify == nil || !verify(ctx, capability) {
		return errors.New("local owner authentication required")
	}
	key, err := d.vscreenResource(workspaceID, runtimeID)
	if err != nil {
		return err
	}
	s := d.vscreenRuntime()
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return errors.New("native host unavailable")
	}
	s.interventions.mu.Lock()
	defer s.interventions.mu.Unlock()
	record := s.interventions.records[runtimeID]
	if record == nil || record.Report.InterventionID != id || record.Report.WorkspaceID != workspaceID || !record.Stopped || len(record.Windows) == 0 {
		return errors.New("stopped native intervention required")
	}
	expected := protocol.VscreenInterventionAwaitingTakeover
	if direction == "to_virtual" {
		expected = protocol.VscreenInterventionHuman
	}
	if record.Report.State != expected {
		return errors.New("invalid intervention transition")
	}
	if reporter == nil || !reporter.Acknowledged(id, expected) {
		return errors.New("report_pending")
	}
	actor, err := s.manager.For(key)
	if err != nil {
		return err
	}
	if actor.Status().Display.Epoch != record.Report.Epoch {
		return errors.New("stale native intervention")
	}
	operation, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = client.QuiesceApps(operation, record.Authority); err != nil {
		return err
	}
	for _, handle := range record.Windows {
		token, err := randomBrokerToken()
		if err != nil {
			return err
		}
		grant := native.HumanGrant{Capability: token, InterventionID: id, WindowHandle: handle, Direction: direction, DestinationSourceID: destination}
		if err = client.GrantHuman(operation, record.Authority, grant, 10*time.Second); err != nil {
			return err
		}
		if _, err = client.TransferApp(operation, record.Authority, grant); err != nil {
			return err
		}
	}
	next := protocol.VscreenInterventionHuman
	if direction == "to_virtual" {
		observer := appcontrol.Authority{Resource: record.Authority.Resource, Epoch: record.Report.Epoch}
		observer.ObserverGrant, err = randomBrokerToken()
		if err != nil {
			return err
		}
		if err = client.GrantObserver(operation, observer, 10*time.Second); err != nil {
			return err
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := client.RevokeObserver(cleanup, observer); err != nil {
				d.logger.Warn("virtual screen return observer revoke failed")
			}
		}()
		for _, handle := range record.Windows {
			observation, observeErr := client.ObserveApp(operation, observer, handle, true)
			if observeErr != nil {
				return observeErr
			}
			if len(observation.PNG) == 0 || observation.Window.Handle != handle || observation.Display.Epoch != record.Report.Epoch {
				return errors.New("fresh native return observation missing")
			}
		}
		next = protocol.VscreenInterventionReadyToContinue
		record.Report.ReturnReceiptID = uuid.NewString()
		record.Report.HumanSummary = summary
	}
	record.Report.State = next
	record.Report.RequestID = uuid.NewString()
	if err = d.persistVscreenInterventionsLocked(s); err != nil {
		return err
	}
	return d.enqueueVscreenIntervention(ctx, record.Report)
}
func (d *Daemon) requestVscreenTakeover(ctx context.Context, c protocol.VscreenCommand, a *vscreen.Actor) error {
	s := d.vscreenRuntime()
	taskID := a.Status().Lease.TaskID
	s.interventions.mu.Lock()
	execution := s.interventions.executions[taskID]
	s.interventions.mu.Unlock()
	if execution == nil {
		return errors.New("no active managed GUI execution")
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	select {
	case <-execution.lifetimeDone:
		return errors.New("task already stopped")
	default:
	}
	execution.freeze(&vscreen.Error{Reason: protocol.VscreenActionUncertainReason})
	return nil
}
