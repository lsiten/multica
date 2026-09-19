package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) vscreenEnvelopeCurrent(e protocol.VscreenEnvelope, g mirrorControlGeneration) bool {
	if e.Validate() != nil {
		return false
	}
	if _, err := d.vscreenResource(e.WorkspaceID, e.RuntimeID); err != nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return g != 0 && g == d.mirrorControlGeneration && d.vscreenServerGeneration != "" && e.DaemonGeneration == d.vscreenServerGeneration
}

func (d *Daemon) sendVscreen(enqueue func([]byte) (*wsOutbound, error), g mirrorControlGeneration, event string, payload any) error {
	if enqueue == nil || !d.mirrorControlGenerationIsCurrent(g) {
		return errWSRPCUnavailable
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	frame, err := json.Marshal(protocol.Message{Type: event, Payload: raw})
	if err != nil {
		return err
	}
	outbound, err := enqueue(frame)
	if err == nil && !d.mirrorControlGenerationIsCurrent(g) {
		outbound.cancel()
	}
	return err
}

func (d *Daemon) handleVscreenQuery(ctx context.Context, msg mirrorOfferMessage) {
	var query protocol.VscreenQuery
	if json.Unmarshal(msg.raw, &query) != nil || !d.vscreenEnvelopeCurrent(query.VscreenEnvelope, msg.controlGeneration) {
		return
	}
	result := protocol.VscreenQueryResult{VscreenEnvelope: query.VscreenEnvelope}
	switch query.Kind {
	case "state":
		state, err := d.vscreenSnapshot(ctx, query.WorkspaceID, query.RuntimeID)
		if err != nil {
			result.Reason = vscreenReason(err)
		} else {
			result.State = &state
		}
	case "sources":
		sources, err := d.vscreenSources(ctx, query.WorkspaceID, query.RuntimeID)
		if err != nil {
			result.Reason = vscreenReason(err)
		} else {
			result.Sources = make([]protocol.VscreenSourceDescriptor, 0, len(sources))
			for _, source := range sources {
				result.Sources = append(result.Sources, protocol.VscreenSourceDescriptor{
					MirrorSourceBinding: source.MirrorSourceBinding,
					DisplayID:           source.DisplayID,
					Name:                source.Name,
					Width:               int(source.Width),
					Height:              int(source.Height),
					LogicalWidth:        source.LogicalWidth,
					LogicalHeight:       source.LogicalHeight,
					Scale:               source.Scale,
					X:                   source.X,
					Y:                   source.Y,
					GeometryRevision:    source.GeometryRevision,
				})
			}
		}
	default:
		return
	}
	if err := d.sendVscreen(msg.enqueue, msg.controlGeneration, protocol.EventVscreenQueryResult, result); err != nil {
		d.logger.Debug("virtual screen query reply dropped")
	}
}

func (d *Daemon) handleVscreenCommand(ctx context.Context, msg mirrorOfferMessage) {
	var command protocol.VscreenCommand
	if json.Unmarshal(msg.raw, &command) != nil || command.Validate() != nil || !d.vscreenEnvelopeCurrent(command.VscreenEnvelope, msg.controlGeneration) {
		return
	}
	s := d.vscreenRuntime()
	s.commandMu.Lock()
	now := time.Now()
	for id, cached := range s.commands {
		if now.After(cached.expires) {
			delete(s.commands, id)
		}
	}
	if cached, ok := s.commands[command.CommandID]; ok {
		s.commandMu.Unlock()
		if cached.command.Kind == command.Kind && cached.command.RuntimeID == command.RuntimeID && cached.command.WorkspaceID == command.WorkspaceID {
			cached.receipt.VscreenEnvelope = command.VscreenEnvelope
			if err := d.sendVscreen(msg.enqueue, msg.controlGeneration, protocol.EventVscreenResult, cached.receipt); err != nil {
				d.logger.Debug("virtual screen cached receipt dropped")
			}
		}
		return
	}
	if len(s.commands) >= 1024 {
		s.commandMu.Unlock()
		return
	}
	receipt := protocol.VscreenCommandReceipt{VscreenEnvelope: command.VscreenEnvelope, CommandID: command.CommandID, ReceiptID: uuid.NewString(), State: protocol.VscreenReceiptPending}
	s.commands[command.CommandID] = vscreenCachedCommand{command: command, receipt: receipt, expires: now.Add(time.Hour)}
	s.commandMu.Unlock()
	if err := d.sendVscreen(msg.enqueue, msg.controlGeneration, protocol.EventVscreenResult, receipt); err != nil {
		return
	}
	err := d.executeVscreenCommand(ctx, command, msg.controlGeneration)
	receipt.State = protocol.VscreenReceiptSucceeded
	if err != nil {
		receipt.State = protocol.VscreenReceiptFailed
		receipt.Reason = vscreenReason(err)
	}
	if err == nil && command.Kind == protocol.VscreenCommandDisable {
		if persistErr := d.retainVscreenCleanup(s, receipt); persistErr != nil {
			receipt.State = protocol.VscreenReceiptFailed
			receipt.Reason = protocol.VscreenNativeUnavailable
		}
	}
	state, stateErr := d.vscreenSnapshot(ctx, command.WorkspaceID, command.RuntimeID)
	if stateErr == nil && state.DisplayGeneration != "" {
		receipt.Epoch = &protocol.VscreenEpoch{NativeEpoch: state.NativeEpoch, DisplayGeneration: state.DisplayGeneration, GeometryRevision: state.GeometryRevision}
	}
	s.commandMu.Lock()
	cached := s.commands[command.CommandID]
	cached.receipt = receipt
	s.commands[command.CommandID] = cached
	s.commandMu.Unlock()
	if err = d.sendVscreen(msg.enqueue, msg.controlGeneration, protocol.EventVscreenResult, receipt); err != nil {
		d.logger.Debug("virtual screen command reply dropped")
	}
}

func (d *Daemon) executeVscreenCommand(ctx context.Context, c protocol.VscreenCommand, g mirrorControlGeneration) error {
	key, err := d.vscreenResource(c.WorkspaceID, c.RuntimeID)
	if err != nil {
		return err
	}
	if !d.vscreenEnvelopeCurrent(c.VscreenEnvelope, g) {
		return errWSRPCUnavailable
	}
	if c.Kind.HostInteractionCommand() {
		return d.executeHostInteractionCommand(ctx, c, g)
	}
	s := d.vscreenRuntime()
	if c.Kind == protocol.VscreenCommandRequestTakeover {
		d.vscreenMu.Lock()
		h := d.vscreenTakeover
		d.vscreenMu.Unlock()
		if h == nil {
			h = d.requestVscreenTakeover
		}
		a, err := s.manager.For(key)
		if err != nil {
			return err
		}
		return h(ctx, c, a)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !d.vscreenEnvelopeCurrent(c.VscreenEnvelope, g) {
		return errWSRPCUnavailable
	}
	a, err := s.manager.For(key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	nativeCtx := context.WithoutCancel(ctx)
	switch c.Kind {
	case protocol.VscreenCommandEnable:
		s.interventions.mu.Lock()
		record := s.interventions.records[c.RuntimeID]
		blocked := record != nil && (record.CleanupReceipt != nil || record.Report.State != protocol.VscreenInterventionContinued)
		s.interventions.mu.Unlock()
		if blocked || a.Status().Stopping && a.Status().Ready {
			return &vscreen.Error{Reason: protocol.VscreenActionUncertainReason}
		}
		if err = d.startVscreenHost(nativeCtx, s); err != nil {
			return err
		}
		if err = d.requestVscreenPermissions(nativeCtx, s, appcontrol.PermissionRequest{Accessibility: true, ScreenRecording: true}); err != nil {
			return err
		}
		display, ensureErr := a.Ensure(nativeCtx)
		if ensureErr != nil {
			return ensureErr
		}
		if a.Status().Stopping {
			if err = a.Recover(nativeCtx, display.Epoch); err != nil {
				return err
			}
		}
		s.enabled[key] = true
	case protocol.VscreenCommandDisable:
		ownerTaskID := a.Status().Lease.TaskID
		if err = a.Dispose(nativeCtx); err != nil {
			return err
		}
		if err = d.stopDisposedVscreenExecution(nativeCtx, s, ownerTaskID); err != nil {
			return err
		}
		delete(s.enabled, key)
	}
	return d.saveVscreenPreferences(s)
}

// requestVscreenPermissions registers the native host with TCC and presents the
// system consent prompts. A denied answer still leaves the virtual display
// available; capture and control enforce their respective permissions.
func (d *Daemon) requestVscreenPermissions(ctx context.Context, s *vscreenRuntime, request appcontrol.PermissionRequest) error {
	if (!request.ScreenRecording || s.screenPermissionRequested) && (!request.Accessibility || s.accessibilityPermissionRequested) {
		return nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	_, err := s.client.RequestAppPermissions(requestCtx, request)
	if err == nil {
		s.screenPermissionRequested = s.screenPermissionRequested || request.ScreenRecording
		s.accessibilityPermissionRequested = s.accessibilityPermissionRequested || request.Accessibility
	}
	return err
}

func (d *Daemon) executeHostInteractionCommand(ctx context.Context, c protocol.VscreenCommand, g mirrorControlGeneration) error {
	switch c.Kind {
	case protocol.VscreenCommandEnableInteraction:
		s := d.vscreenRuntime()
		nativeCtx := context.WithoutCancel(ctx)
		s.mu.Lock()
		err := d.startVscreenHost(nativeCtx, s)
		if err == nil {
			err = d.requestVscreenPermissions(nativeCtx, s, appcontrol.PermissionRequest{Accessibility: true})
		}
		s.mu.Unlock()
		if err != nil {
			return err
		}
		d.SetHumanInteractionEnabled(true)
		return nil
	case protocol.VscreenCommandDisableInteraction:
		d.SetHumanInteractionEnabled(false)
		return nil
	case protocol.VscreenCommandEmergencyStop:
		d.SetHumanInteractionEnabled(false)
		if d.inputArbiter != nil {
			d.inputArbiter.EmergencyReleaseRuntime(c.WorkspaceID, c.RuntimeID)
		}
		if rm := d.existingManagedMirror(c.RuntimeID, g); rm != nil {
			rm.RevokeAllControl(uint64(g))
		}
		return nil
	default:
		return protocol.ErrInvalidVscreenContract
	}
}

type vscreenCachedCommand struct {
	command protocol.VscreenCommand
	receipt protocol.VscreenCommandReceipt
	expires time.Time
}
