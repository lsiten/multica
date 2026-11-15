package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type vscreenGrantKey struct{ runtime, session, viewer, grant string }
type vscreenGrantEntry struct {
	expires time.Time
	cancel  context.CancelFunc
	revoked bool
}

func grantKey(g protocol.MirrorViewerGrant) vscreenGrantKey {
	return vscreenGrantKey{g.RuntimeID, g.SessionID, g.ViewerID, g.GrantID}
}

func (d *Daemon) mirrorEnvelopeCurrent(e protocol.RuntimeEnvelope, generation mirrorConnectionGeneration) bool {
	if e.Validate() != nil {
		return false
	}
	if _, err := d.vscreenResource(e.WorkspaceID, e.RuntimeID); err != nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return generation != 0 && generation == d.mirrorConnectionGeneration && d.vscreenServerGeneration != "" && e.DaemonGeneration == d.vscreenServerGeneration
}

func (d *Daemon) sendVscreen(enqueue func([]byte) (*wsOutbound, error), generation mirrorConnectionGeneration, event string, payload any) error {
	if enqueue == nil || !d.mirrorConnectionCurrent(generation) {
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
	if err == nil && !d.mirrorConnectionCurrent(generation) {
		outbound.cancel()
	}
	return err
}

func (d *Daemon) handleManagedMirrorOffer(ctx context.Context, msg mirrorOfferMessage) {
	var offer protocol.MirrorOfferPayload
	if json.Unmarshal(msg.raw, &offer) != nil || offer.Validate() != nil || offer.ViewerGrant == nil {
		return
	}
	envelope := protocol.RuntimeEnvelope{WorkspaceID: offer.WorkspaceID, RuntimeID: offer.RuntimeID, RequestID: offer.SessionID, DaemonGeneration: offer.DaemonGeneration}
	if !d.mirrorEnvelopeCurrent(envelope, msg.controlGeneration) || offer.DaemonID != d.cfg.DaemonID {
		return
	}
	s := d.vscreenRuntime()
	s.grantMu.Lock()
	now := time.Now()
	for key, entry := range s.grants {
		if !entry.expires.After(now) {
			delete(s.grants, key)
		}
	}
	key := grantKey(*offer.ViewerGrant)
	if _, exists := s.grants[key]; exists || len(s.grants) >= 1024 || s.grantClosed || s.grantBlockedUntil.After(now) {
		s.grantMu.Unlock()
		return
	}
	answerCtx, cancel := mirrorOfferContext(ctx, offer.ExpiresAt)
	s.grantWG.Add(1)
	s.grants[key] = vscreenGrantEntry{expires: maxTime(offer.ViewerGrant.ExpiresAt, offer.ExpiresAt), cancel: cancel}
	s.grantMu.Unlock()
	// Negotiation belongs to the authenticated connection; revoke cancels it even
	// before Pion installs its peer, and the tombstone survives until offer expiry.
	go func() {
		defer s.grantWG.Done()
		defer cancel()
		s.mu.Lock()
		err := d.startVscreenHost(answerCtx, s)
		s.mu.Unlock()
		if err != nil {
			d.managedMirrorFailure(msg, offer, err)
			return
		}
		sources, err := d.vscreenSources(answerCtx, offer.WorkspaceID, offer.RuntimeID)
		if err != nil {
			d.managedMirrorFailure(msg, offer, err)
			return
		}
		resource, err := d.vscreenResource(offer.WorkspaceID, offer.RuntimeID)
		if err != nil {
			return
		}
		resource, authorized := mirrorOfferResource(resource, sources, *offer.ViewerGrant)
		if !authorized {
			d.managedMirrorFailure(msg, offer, protocol.ErrInvalidMirrorDescription)
			return
		}
		bindings := make([]protocol.MirrorSourceBinding, 0, len(sources))
		for _, source := range sources {
			bindings = append(bindings, source.MirrorSourceBinding)
		}
		_, err = protocol.ParseMirrorOffer(msg.raw, protocol.MirrorAuthorization{Resource: resource, DaemonID: d.cfg.DaemonID, UserID: offer.UserID, NativeEpoch: offer.ViewerGrant.NativeEpoch, Now: time.Now(), Sources: bindings, RequireViewerGrant: true})
		if err != nil {
			d.managedMirrorFailure(msg, offer, err)
			return
		}
		rm, ok := d.managedRuntimeMirror(offer.RuntimeID, msg.controlGeneration, sources)
		if !ok {
			return
		}
		s.mu.Lock()
		hub := s.hub
		s.mu.Unlock()
		rm.SetCaptureHub(hub)
		d.bindMirrorStateHooks(rm, msg.enqueue, offer.WorkspaceID, offer.RuntimeID, msg.controlGeneration)
		var answer mirror.Negotiation
		if offer.ProtocolVersion == 2 {
			for _, source := range sources {
				if source.Source == *offer.Source {
					selection := mirrorVideoSelection(source, offer.VideoProfile)
					answer, err = rm.AnswerVideo(answerCtx, offer.ViewerID, mirror.SessionDescription{Type: offer.Offer.Type, SDP: offer.Offer.SDP}, mirror.ICEConfigFromProtocol(offer.ICEConfig), selection, *offer.ViewerGrant, uint64(msg.controlGeneration))
					break
				}
			}
		} else {
			answer, err = rm.Answer(answerCtx, offer.ViewerID, mirror.SessionDescription{Type: offer.Offer.Type, SDP: offer.Offer.SDP}, mirror.ICEConfigFromProtocol(offer.ICEConfig))
			if err == nil && !rm.BindViewerGrant(offer.ViewerID, *offer.ViewerGrant, uint64(msg.controlGeneration)) {
				err = mirror.ErrViewerClosed
			}
		}
		if err != nil {
			answer.Abandon()
			d.managedMirrorFailure(msg, offer, err)
			return
		}
		s.grantMu.Lock()
		entry, exists := s.grants[key]
		allowed := exists && !entry.revoked && answerCtx.Err() == nil && d.mirrorEnvelopeCurrent(envelope, msg.controlGeneration)
		if allowed {
			answer.Commit()
		}
		s.grantMu.Unlock()
		if !allowed {
			answer.Abandon()
			return
		}
		payload := protocol.MirrorAnswerPayload{ProtocolVersion: offer.ProtocolVersion, Transport: offer.Transport, Source: offer.Source, SourceGeneration: offer.SourceGeneration, NativeEpoch: offer.NativeEpoch, ViewerGrant: offer.ViewerGrant, SessionID: offer.SessionID, WorkspaceID: offer.WorkspaceID, RuntimeID: offer.RuntimeID, UserID: offer.UserID, DaemonID: offer.DaemonID, ViewerID: offer.ViewerID, Answer: protocol.MirrorSessionDescription{Type: answer.Type, SDP: answer.SDP}, ExpiresAt: offer.ExpiresAt, VideoQuality: answer.VideoQuality}
		if err = d.sendVscreen(msg.enqueue, msg.controlGeneration, protocol.EventMirrorAnswer, payload); err != nil {
			answer.Abandon()
		}
	}()
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func (d *Daemon) managedMirrorFailure(msg mirrorOfferMessage, offer protocol.MirrorOfferPayload, err error) {
	reason := protocol.MirrorAnswerFailureCaptureUnavailable
	if mirrorFailureReason(err) == "permission_denied" {
		reason = protocol.MirrorAnswerFailurePermissionDenied
	}
	if d.mirrorConnectionCurrent(msg.controlGeneration) {
		if err = d.sendMirrorAnswerFailure(msg.enqueue, offer, reason); err != nil {
			d.logger.Debug("managed mirror failure dropped")
		}
	}
}

func (d *Daemon) handleVscreenViewerRevoke(msg mirrorOfferMessage) {
	if d.forwardMirrorControl(context.Background(), protocol.EventMirrorViewerRevoke, msg) {
		return
	}
	var p protocol.MirrorViewerRevokePayload
	if json.Unmarshal(msg.raw, &p) != nil || p.GrantID == "" || p.SessionID == "" || p.ViewerID == "" || !d.mirrorEnvelopeCurrent(protocol.RuntimeEnvelope{WorkspaceID: p.WorkspaceID, RuntimeID: p.RuntimeID, DaemonGeneration: p.DaemonGeneration, RequestID: p.GrantID}, msg.controlGeneration) {
		return
	}
	s := d.vscreenRuntime()
	key := vscreenGrantKey{p.RuntimeID, p.SessionID, p.ViewerID, p.GrantID}
	s.grantMu.Lock()
	entry, exists := s.grants[key]
	if !exists {
		entry.expires = time.Now().Add(time.Minute)
	}
	entry.revoked = true
	if entry.cancel != nil {
		entry.cancel()
	}
	if exists || len(s.grants) < 1024 {
		s.grants[key] = entry
	} else {
		s.grantBlockedUntil = time.Now().Add(time.Minute)
	}
	s.grantMu.Unlock()
	if rm := d.existingManagedMirror(p.RuntimeID, msg.controlGeneration); rm != nil {
		rm.RevokeViewerGrant(p.ViewerID, p.GrantID, uint64(msg.controlGeneration))
	}
}
func (d *Daemon) handleVscreenViewerRenew(msg mirrorOfferMessage) {
	if d.forwardMirrorControl(context.Background(), protocol.EventMirrorViewerRenew, msg) {
		return
	}
	var p protocol.MirrorViewerRenewPayload
	if json.Unmarshal(msg.raw, &p) != nil || !d.mirrorEnvelopeCurrent(protocol.RuntimeEnvelope{WorkspaceID: p.Grant.WorkspaceID, RuntimeID: p.Grant.RuntimeID, DaemonGeneration: p.DaemonGeneration, RequestID: p.Grant.GrantID}, msg.controlGeneration) {
		return
	}
	s := d.vscreenRuntime()
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	entry := s.grants[grantKey(p.Grant)]
	if entry.revoked {
		return
	}
	if rm := d.existingManagedMirror(p.Grant.RuntimeID, msg.controlGeneration); rm != nil && rm.RenewViewerGrant(p.Grant.ViewerID, p.Grant, uint64(msg.controlGeneration)) {
		entry.expires = p.Grant.ExpiresAt
		s.grants[grantKey(p.Grant)] = entry
	}
}

func (d *Daemon) existingManagedMirror(runtimeID string, g mirrorConnectionGeneration) *mirror.RuntimeMirror {
	d.mu.Lock()
	defer d.mu.Unlock()
	if g == 0 || g != d.mirrorConnectionGeneration {
		return nil
	}
	return d.runtimeMirrors[runtimeID]
}
