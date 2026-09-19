package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/mirror"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) configureMirrorGrant(w http.ResponseWriter, r *http.Request, rt db.AgentRuntime, req createMirrorSessionRequest, payload *protocol.MirrorOfferPayload) (string, bool) {
	managed := runtimeHasCapability(rt.Metadata, protocol.DaemonCapabilityMirrorViewerGrantV1)
	if !managed {
		if req.ProtocolVersion > 1 || req.Source != nil || req.Transport != "" || req.SourceGeneration != "" {
			writeVscreenReason(w, http.StatusNotImplemented, "upgrade_required")
			return "", false
		}
		return "", true
	}
	credential, ok := middleware.ViewerCredentialFromContext(r.Context())
	if !ok || credential.UserID != payload.UserID {
		writeVscreenReason(w, http.StatusForbidden, "viewer_credential_required")
		return "", false
	}
	if req.ProtocolVersion != 0 && req.ProtocolVersion != 1 && req.ProtocolVersion != 2 {
		writeVscreenReason(w, http.StatusBadRequest, "invalid_mirror_protocol")
		return "", false
	}
	if req.ProtocolVersion == 2 && (req.Transport != "video" || req.Source == nil || !runtimeHasCapability(rt.Metadata, protocol.DaemonCapabilityScreenMirrorVideoV2)) {
		writeVscreenReason(w, http.StatusBadRequest, "invalid_mirror_protocol")
		return "", false
	}
	if req.ProtocolVersion != 2 && (req.Source != nil || req.Transport != "" || req.SourceGeneration != "") {
		writeVscreenReason(w, http.StatusBadRequest, "invalid_mirror_protocol")
		return "", false
	}
	catalog, err := h.DaemonHub.QueryVscreen(r.Context(), payload.WorkspaceID, payload.RuntimeID, payload.DaemonID, "sources")
	if err != nil {
		writeVscreenError(w, err)
		return "", false
	}
	if catalog.Reason != "" {
		writeVscreenReason(w, http.StatusConflict, string(catalog.Reason))
		return "", false
	}
	var selected *protocol.MirrorSourceBinding
	staleGeneration := false
	for _, descriptor := range catalog.Sources {
		binding := descriptor.MirrorSourceBinding
		if req.ProtocolVersion == 2 {
			if binding.Source == *req.Source && binding.Generation != req.SourceGeneration {
				staleGeneration = true
			}
			if binding.Source == *req.Source && binding.Generation == req.SourceGeneration {
				selected = &binding
				break
			}
		} else if binding.Primary && binding.Source.Kind != protocol.MirrorSourceVirtual {
			selected = &binding
			break
		}
	}
	if selected == nil {
		if staleGeneration {
			writeVscreenReason(w, http.StatusConflict, "stale_generation")
			return "", false
		}
		writeVscreenReason(w, http.StatusConflict, "source_unavailable")
		return "", false
	}
	now := time.Now()
	expiry := now.Add(30 * time.Second)
	if !credential.ExpiresAt.IsZero() && credential.ExpiresAt.Before(expiry) {
		expiry = credential.ExpiresAt
	}
	grant := protocol.MirrorViewerGrant{GrantID: uuid.NewString(), SessionID: payload.SessionID, WorkspaceID: payload.WorkspaceID, RuntimeID: payload.RuntimeID, UserID: payload.UserID, ViewerID: payload.ViewerID, NativeEpoch: selected.NativeEpoch, Source: selected.Source, SourceGeneration: selected.Generation, ExpiresAt: expiry}
	record := mirror.ViewerGrantRecord{Grant: grant, DaemonID: payload.DaemonID, DaemonGeneration: catalog.DaemonGeneration, CredentialHash: credential.Hash, CredentialKind: credential.Kind, CredentialExpiry: credential.ExpiresAt, Legacy: req.ProtocolVersion != 2}
	if err := h.refreshViewerCredential(r.Context(), &record); err != nil {
		writeVscreenReason(w, http.StatusForbidden, "viewer_revoked")
		return "", false
	}
	if !record.CredentialExpiry.IsZero() && record.CredentialExpiry.Before(grant.ExpiresAt) {
		grant.ExpiresAt = record.CredentialExpiry
	}
	record.Grant = grant
	if err := h.MirrorGrants.Add(record, time.Now()); err != nil {
		h.writeMirrorSessionError(w, err)
		return "", false
	}
	payload.ViewerGrant = &grant
	payload.DaemonGeneration = catalog.DaemonGeneration
	if req.ProtocolVersion == 2 {
		payload.ProtocolVersion = 2
		payload.Transport = "video"
		payload.Source = &selected.Source
		payload.SourceGeneration = selected.Generation
		payload.NativeEpoch = selected.NativeEpoch
	}
	return catalog.DaemonGeneration, true
}

// RenewMirrorSession rechecks the authenticated viewer and current runtime read gate.
func (h *Handler) RenewMirrorSession(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "mirror_renew", chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	record, err := h.MirrorGrants.Lookup(chi.URLParam(r, "sessionId"), requestUserID(r), uuidToString(rt.ID), time.Now())
	if err != nil {
		h.writeMirrorSessionError(w, err)
		return
	}
	credential, ok := middleware.ViewerCredentialFromContext(r.Context())
	if !ok || credential.Hash != record.CredentialHash || credential.UserID != record.Grant.UserID || !record.Active {
		writeVscreenReason(w, http.StatusForbidden, "viewer_not_active")
		return
	}
	renewed, err := h.renewViewerGrant(r.Context(), record)
	if err != nil {
		h.revokeViewerGrant(record)
		writeVscreenReason(w, http.StatusForbidden, "viewer_revoked")
		return
	}
	writeJSON(w, http.StatusOK, renewed.Grant)
}

func (h *Handler) revokeViewerGrant(record mirror.ViewerGrantRecord) {
	if _, ok := h.MirrorGrants.RemoveIfCurrent(record); !ok {
		return
	}
	h.sendViewerRevoke(record)
	h.revokeControlGrantViewer(record.Grant.RuntimeID, record.Grant.ViewerID)
}

func (h *Handler) sendViewerRevoke(record mirror.ViewerGrantRecord) {
	h.DaemonHub.SendViewerRevoke(record.DaemonID, protocol.MirrorViewerRevokePayload{WorkspaceID: record.Grant.WorkspaceID, RuntimeID: record.Grant.RuntimeID, DaemonGeneration: record.DaemonGeneration, SessionID: record.Grant.SessionID, ViewerID: record.Grant.ViewerID, GrantID: record.Grant.GrantID})
}
