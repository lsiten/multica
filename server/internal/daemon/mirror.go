package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const mirrorAnswerMargin = 2 * time.Second

type mirrorOfferMessage struct {
	raw               json.RawMessage
	enqueue           func([]byte) (*wsOutbound, error)
	controlGeneration mirrorControlGeneration
}

func (d *Daemon) handleMirrorOffer(ctx context.Context, message mirrorOfferMessage) {
	raw := message.raw
	enqueue := message.enqueue
	controlGeneration := message.controlGeneration
	if enqueue == nil {
		return
	}
	var offer protocol.MirrorOfferPayload
	if err := json.Unmarshal(raw, &offer); err != nil || offer.Validate() != nil {
		d.logger.Debug("mirror offer rejected")
		return
	}
	if offer.DaemonID != d.cfg.DaemonID || offer.RuntimeID == "" {
		d.logger.Debug("mirror offer identity rejected", "runtime_id", offer.RuntimeID)
		return
	}
	if d.findRuntime(offer.RuntimeID) == nil {
		d.logger.Debug("mirror offer runtime unavailable", "runtime_id", offer.RuntimeID)
		return
	}
	if !mirror.NativeCaptureSupported() {
		d.logger.Debug("mirror offer unsupported on platform", "runtime_id", offer.RuntimeID)
		if err := d.sendMirrorAnswerFailure(enqueue, offer, protocol.MirrorAnswerFailureUnsupported); err != nil {
			d.logger.Debug("mirror answer failure dropped", "runtime_id", offer.RuntimeID, "error", err)
		}
		return
	}
	go func() {
		runtimeMirror, created, ok := d.runtimeMirrorForOffer(offer.RuntimeID, controlGeneration)
		if !ok {
			d.logger.Debug("stale mirror offer ignored", "runtime_id", offer.RuntimeID)
			if sendErr := d.sendMirrorAnswerFailure(enqueue, offer, protocol.MirrorAnswerFailureNegotiation); sendErr != nil {
				d.logger.Debug("stale mirror answer failure dropped", "runtime_id", offer.RuntimeID, "error", sendErr)
			}
			return
		}
		if created {
			runtimeMirror.SetViewerStateHook(func(change mirror.ViewerStateChange) {
				if _, err := d.sendMirrorViewerState(enqueue, offer.WorkspaceID, offer.RuntimeID, change.ViewerID, change.Active); err != nil {
					d.logger.Debug("mirror viewer state dropped", "runtime_id", offer.RuntimeID, "error", err)
				}
			}, uint64(controlGeneration))
		}
		answerCtx, cancel := mirrorOfferContext(ctx, offer.ExpiresAt)
		defer cancel()
		answer, err := runtimeMirror.Answer(answerCtx, offer.ViewerID, mirror.SessionDescription{
			Type: offer.Offer.Type,
			SDP:  offer.Offer.SDP,
		}, mirror.ICEConfigFromProtocol(offer.ICEConfig))
		if err != nil {
			d.logger.Debug("mirror offer negotiation failed", "runtime_id", offer.RuntimeID, "error", err)
			if sendErr := d.sendMirrorAnswerFailure(enqueue, offer, mirrorAnswerFailureReason(err)); sendErr != nil {
				d.logger.Debug("mirror answer failure dropped", "runtime_id", offer.RuntimeID, "error", sendErr)
			}
			return
		}
		if !d.mirrorControlGenerationIsCurrent(controlGeneration) {
			if err := answer.Abandon(); err != nil {
				d.logger.Debug("stale mirror peer close failed", "runtime_id", offer.RuntimeID, "error", err)
			}
			if sendErr := d.sendMirrorAnswerFailure(enqueue, offer, protocol.MirrorAnswerFailureNegotiation); sendErr != nil {
				d.logger.Debug("stale mirror answer failure dropped", "runtime_id", offer.RuntimeID, "error", sendErr)
			}
			return
		}
		payload := protocol.MirrorAnswerPayload{
			SessionID: offer.SessionID, WorkspaceID: offer.WorkspaceID, RuntimeID: offer.RuntimeID,
			UserID: offer.UserID, DaemonID: offer.DaemonID, ViewerID: offer.ViewerID,
			Answer:    protocol.MirrorSessionDescription{Type: answer.Type, SDP: answer.SDP},
			ExpiresAt: offer.ExpiresAt,
		}
		frame, err := json.Marshal(protocol.Message{Type: protocol.EventMirrorAnswer, Payload: marshalRaw(payload)})
		if err != nil {
			if abandonErr := answer.Abandon(); abandonErr != nil {
				d.logger.Debug("mirror answer peer close failed", "runtime_id", offer.RuntimeID, "error", abandonErr)
			}
			return
		}
		if !d.mirrorControlGenerationIsCurrent(controlGeneration) {
			if err := answer.Abandon(); err != nil {
				d.logger.Debug("stale mirror peer close failed", "runtime_id", offer.RuntimeID, "error", err)
			}
			if sendErr := d.sendMirrorAnswerFailure(enqueue, offer, protocol.MirrorAnswerFailureNegotiation); sendErr != nil {
				d.logger.Debug("stale mirror answer failure dropped", "runtime_id", offer.RuntimeID, "error", sendErr)
			}
			return
		}
		answer.Commit()
		outbound, err := enqueue(frame)
		if err != nil {
			if abandonErr := answer.Abandon(); abandonErr != nil {
				d.logger.Debug("mirror answer peer close failed", "runtime_id", offer.RuntimeID, "error", abandonErr)
			}
			d.logger.Debug("mirror answer dropped", "runtime_id", offer.RuntimeID, "error", err)
			return
		}
		if !d.mirrorControlGenerationIsCurrent(controlGeneration) && outbound.cancel() {
			if err := answer.Abandon(); err != nil {
				d.logger.Debug("stale mirror peer close failed", "runtime_id", offer.RuntimeID, "error", err)
			}
		}
	}()
}

func mirrorAnswerFailureReason(err error) string {
	var captureErr *mirror.CaptureError
	if errors.As(err, &captureErr) {
		switch captureErr.Reason {
		case mirror.ControlReasonPermissionDenied:
			return protocol.MirrorAnswerFailurePermissionDenied
		case mirror.ControlReasonUnsupported:
			return protocol.MirrorAnswerFailureUnsupported
		case mirror.ControlReasonNoDisplay:
			return protocol.MirrorAnswerFailureNoDisplay
		case mirror.ControlReasonCaptureUnavailable:
			return protocol.MirrorAnswerFailureCaptureUnavailable
		}
	}
	return protocol.MirrorAnswerFailureNegotiation
}

func (d *Daemon) sendMirrorAnswerFailure(
	enqueue func([]byte) (*wsOutbound, error),
	offer protocol.MirrorOfferPayload,
	reason string,
) error {
	if enqueue == nil {
		return errWSRPCUnavailable
	}
	payload := protocol.MirrorAnswerFailurePayload{
		SessionID:   offer.SessionID,
		WorkspaceID: offer.WorkspaceID,
		RuntimeID:   offer.RuntimeID,
		UserID:      offer.UserID,
		DaemonID:    offer.DaemonID,
		ViewerID:    offer.ViewerID,
		Reason:      reason,
		ExpiresAt:   offer.ExpiresAt,
	}
	frame, err := json.Marshal(protocol.Message{Type: protocol.EventMirrorAnswerFailure, Payload: marshalRaw(payload)})
	if err != nil {
		return err
	}
	_, err = enqueue(frame)
	return err
}

func (d *Daemon) sendMirrorViewerState(
	enqueue func([]byte) (*wsOutbound, error),
	workspaceID string,
	runtimeID string,
	viewerID string,
	active bool,
) (*wsOutbound, error) {
	if enqueue == nil {
		return nil, errWSRPCUnavailable
	}
	payload := protocol.MirrorViewerPayload{
		WorkspaceID: workspaceID,
		RuntimeID:   runtimeID,
		DaemonID:    d.cfg.DaemonID,
		ViewerID:    viewerID,
		Active:      active,
	}
	frame, err := json.Marshal(protocol.Message{Type: protocol.EventMirrorViewer, Payload: marshalRaw(payload)})
	if err != nil {
		return nil, err
	}
	return enqueue(frame)
}

func mirrorOfferContext(parent context.Context, expiresAt time.Time) (context.Context, context.CancelFunc) {
	return context.WithDeadline(parent, expiresAt.Add(-mirrorAnswerMargin))
}

type trackedRuntimeMirror struct {
	workspaceID string
	runtimeID   string
	mirror      *mirror.RuntimeMirror
}

func (d *Daemon) trackedRuntimeMirrors() []trackedRuntimeMirror {
	d.mu.Lock()
	workspaceByRuntime := make(map[string]string, len(d.runtimeMirrors))
	for workspaceID, ws := range d.workspaces {
		for _, runtimeID := range ws.runtimeIDs {
			workspaceByRuntime[runtimeID] = workspaceID
		}
	}
	tracked := make([]trackedRuntimeMirror, 0, len(d.runtimeMirrors))
	for runtimeID, runtimeMirror := range d.runtimeMirrors {
		workspaceID := workspaceByRuntime[runtimeID]
		if workspaceID == "" {
			continue
		}
		tracked = append(tracked, trackedRuntimeMirror{
			workspaceID: workspaceID,
			runtimeID:   runtimeID,
			mirror:      runtimeMirror,
		})
	}
	d.mu.Unlock()
	return tracked
}
