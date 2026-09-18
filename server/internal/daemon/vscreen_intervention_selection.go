package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/vscreen/hostclient"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// selectVscreenWindow is reachable only through the authenticated local-owner bridge.
// Candidate metadata is returned directly; only a successfully adopted opaque handle is persisted.
func (d *Daemon) selectVscreenWindow(ctx context.Context, capability, workspaceID, runtimeID, id, handle string, adopt bool) (*appcontrol.WindowCandidates, error) {
	d.vscreenMu.Lock()
	verify, reporter := d.vscreenLocalOwner, d.vscreenReporter
	d.vscreenMu.Unlock()
	if verify == nil || !verify(ctx, capability) {
		return nil, errors.New("local_owner_required")
	}
	if id == "" || len(id) > 128 || len(handle) > 128 || adopt && handle == "" || !adopt && handle != "" {
		return nil, errors.New("invalid_selection")
	}
	key, err := d.vscreenResource(workspaceID, runtimeID)
	if err != nil {
		return nil, errors.New("runtime_not_local")
	}
	s := d.vscreenRuntime()
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return nil, errors.New("native_unavailable")
	}
	s.interventions.mu.Lock()
	defer s.interventions.mu.Unlock()
	record := s.interventions.records[runtimeID]
	if record == nil || record.Report.InterventionID != id || record.Report.WorkspaceID != workspaceID || !record.Stopped || record.Report.State != protocol.VscreenInterventionAwaitingTakeover || len(record.Windows) != 0 {
		return nil, errors.New("selection_unavailable")
	}
	if reporter == nil || !reporter.Acknowledged(id, record.Report.State) {
		return nil, errors.New("report_pending")
	}
	actor, err := s.manager.For(key)
	if err != nil {
		return nil, err
	}
	status := actor.Status()
	if !status.Frozen || status.Display.Epoch != record.Report.Epoch || record.Authority.Resource != key || record.Authority.Epoch != record.Report.Epoch {
		return nil, errors.New("stale_intervention")
	}
	operation, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	current, err := client.Call(context.WithoutCancel(operation), native.Request{Operation: "describe", Resource: key, Epoch: record.Report.Epoch})
	if err != nil || current.Display == nil || current.Epoch != record.Report.Epoch {
		return nil, errors.New("stale_intervention")
	}
	if err = operation.Err(); err != nil {
		return nil, err
	}
	if err = client.QuiesceApps(operation, record.Authority); err != nil {
		return nil, err
	}
	token, err := randomBrokerToken()
	if err != nil {
		return nil, err
	}
	direction := "list_existing"
	if adopt {
		direction = "adopt_existing"
	}
	grant := native.HumanGrant{Capability: token, InterventionID: id, WindowHandle: handle, Direction: direction}
	if err = client.GrantHuman(operation, record.Authority, grant, 10*time.Second); err != nil {
		return nil, err
	}
	if !adopt {
		candidates, err := client.ListAppWindows(operation, record.Authority, grant)
		if err != nil {
			return nil, err
		}
		if err = candidates.Validate(); err != nil {
			return nil, err
		}
		return &candidates, nil
	}
	window, err := client.AdoptAppWindow(operation, record.Authority, grant)
	if err != nil {
		return nil, err
	}
	after := actor.Status()
	if window.Handle == "" || !after.Frozen || after.Display.Epoch != record.Report.Epoch {
		return nil, errors.New("adoption_unconfirmed")
	}
	record.Windows = []string{window.Handle}
	record.Report.State = protocol.VscreenInterventionHuman
	record.Report.RequestID = uuid.NewString()
	if err = d.persistVscreenInterventionsLocked(s); err != nil {
		return nil, err
	}
	return nil, d.enqueueVscreenIntervention(ctx, record.Report)
}
func selectionReason(err error) string {
	var nativeError *hostclient.RemoteError
	if errors.As(err, &nativeError) {
		switch nativeError.Code {
		case "stale_window", "app_claim_conflict", "process_changed":
			return "selection_expired"
		case "accessibility_denied":
			return "accessibility_denied"
		}
	}
	switch err.Error() {
	case "local_owner_required", "runtime_not_local", "report_pending", "selection_unavailable", "stale_intervention", "invalid_selection":
		return err.Error()
	}
	return "handoff_failed"
}
