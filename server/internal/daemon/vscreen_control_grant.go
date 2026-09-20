package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// handleMirrorControlGrant binds or renews one viewer's human input capability.
// It is delivered over the authenticated control WebSocket and pinned to the
// current control generation. The grant never carries input payloads.
func (d *Daemon) handleMirrorControlGrant(msg mirrorOfferMessage) {
	var p protocol.MirrorControlGrantPayload
	if json.Unmarshal(msg.raw, &p) != nil || p.Grant.Validate(time.Now()) != nil {
		d.logger.Debug("mirror control grant rejected")
		return
	}
	if !d.vscreenEnvelopeCurrent(protocol.VscreenEnvelope{
		WorkspaceID:      p.WorkspaceID,
		RuntimeID:        p.RuntimeID,
		DaemonGeneration: p.DaemonGeneration,
		RequestID:        p.Grant.SessionID,
	}, msg.controlGeneration) {
		d.logger.Debug("stale mirror control grant ignored", "runtime_id", p.RuntimeID)
		return
	}
	rm := d.existingManagedMirror(p.RuntimeID, msg.controlGeneration)
	if rm == nil {
		d.logger.Debug("mirror control grant for unknown mirror", "runtime_id", p.RuntimeID)
		return
	}
	if current, ok := rm.CurrentControlGrant(p.Grant.ViewerID); ok {
		generation := uint64(msg.controlGeneration)
		if current.EqualIdentity(p.Grant) {
			if !rm.RenewControlGrant(p.Grant.ViewerID, p.Grant, generation) {
				d.logger.Debug("mirror control grant renewal rejected", "runtime_id", p.RuntimeID)
			}
		} else if !rm.ReplaceControlGrant(p.Grant.ViewerID, p.Grant, generation) {
			d.logger.Debug("mirror control grant replacement rejected", "runtime_id", p.RuntimeID)
		}
		return
	}
	if !rm.BindControlGrant(p.Grant.ViewerID, p.Grant, uint64(msg.controlGeneration)) {
		d.logger.Debug("mirror control grant bind rejected", "runtime_id", p.RuntimeID)
	}
}

func (d *Daemon) handleMirrorControlRevoke(msg mirrorOfferMessage) {
	var p protocol.MirrorControlRevokePayload
	if json.Unmarshal(msg.raw, &p) != nil || p.RuntimeID == "" || p.ViewerID == "" || p.GrantID == "" {
		return
	}
	if !d.vscreenEnvelopeCurrent(protocol.VscreenEnvelope{
		WorkspaceID:      p.WorkspaceID,
		RuntimeID:        p.RuntimeID,
		DaemonGeneration: p.DaemonGeneration,
		RequestID:        p.GrantID,
	}, msg.controlGeneration) {
		return
	}
	if rm := d.existingManagedMirror(p.RuntimeID, msg.controlGeneration); rm != nil {
		rm.RevokeControlGrant(p.ViewerID, p.GrantID, uint64(msg.controlGeneration))
	}
}

// wireMirrorControl installs the shared arbiter and control backend used to
// authorize and execute human input on one runtime mirror.
func (d *Daemon) wireMirrorControl(rm *mirror.RuntimeMirror) {
	if rm == nil {
		return
	}
	if executable := os.Getenv("MULTICA_VOICE_TRANSCRIBER"); executable != "" {
		if transcriber, err := mirror.NewCommandVoiceTranscriber(executable); err == nil {
			rm.SetVoiceTranscriber(transcriber)
		}
	}
	rm.SetArbiter(d.inputArbiter)
	if d.controlBackend == nil {
		d.controlBackend = newMirrorControlBackend(d)
	}
	rm.SetControlBackend(d.controlBackend)
	rm.SetAuthorizationPublisher(func(viewerID string, request protocol.MirrorAuthorizationRequest) {
		if !rm.PublishAuthorizationRequest(viewerID, request) {
			d.logger.Debug("mirror authorization prompt could not be delivered", "runtime_id", request.RequestID)
		}
	})
	// A viewer decision is the final human confirmation for a host permission
	// prompt. The prompt is issued by the trusted native host; no input payload
	// or authorization text is forwarded to the server.
	rm.SetAuthorizationHandler(d.handleMirrorAuthorization)
}

func (d *Daemon) handleMirrorAuthorization(ctx context.Context, request protocol.MirrorAuthorizationRequest, approved bool) error {
	if request.Kind != "system" {
		return errors.New("mirror: CLI approval requires a provider decision handler")
	}
	if !approved {
		return nil
	}
	s := d.vscreenRuntime()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return errors.New("mirror: native host unavailable")
	}
	return d.requestVscreenPermissions(ctx, s, appcontrol.PermissionRequest{Accessibility: true})
}
