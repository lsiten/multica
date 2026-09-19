package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"

	"github.com/multica-ai/multica/server/internal/mirror"
)

// controlGrantTTL is short; the viewer renews while interaction mode stays on.
const controlGrantTTL = 30 * time.Second

type createControlGrantRequest struct {
	ViewerID         string                 `json:"viewer_id"`
	Source           *protocol.MirrorSource `json:"source"`
	SourceGeneration string                 `json:"source_generation,omitempty"`
}

// resolveControlSource verifies the requested source against the live native
// catalog and returns its exact binding plus the current daemon generation.
func (h *Handler) resolveControlSource(w http.ResponseWriter, r *http.Request, rt db.AgentRuntime, req createControlGrantRequest) (protocol.MirrorSourceBinding, string, bool) {
	catalog, err := h.DaemonHub.QueryVscreen(r.Context(), uuidToString(rt.WorkspaceID), uuidToString(rt.ID), rt.DaemonID.String, "sources")
	if err != nil {
		writeVscreenError(w, err)
		return protocol.MirrorSourceBinding{}, "", false
	}
	if catalog.Reason != "" {
		writeVscreenReason(w, http.StatusConflict, string(catalog.Reason))
		return protocol.MirrorSourceBinding{}, "", false
	}
	var selected *protocol.MirrorSourceBinding
	stale := false
	for i := range catalog.Sources {
		binding := catalog.Sources[i].MirrorSourceBinding
		if binding.Source == *req.Source {
			if req.SourceGeneration != "" && binding.Generation != req.SourceGeneration {
				stale = true
				continue
			}
			b := binding
			selected = &b
			break
		}
	}
	if selected == nil {
		if stale {
			writeVscreenReason(w, http.StatusConflict, "stale_generation")
		} else {
			writeVscreenReason(w, http.StatusConflict, "source_unavailable")
		}
		return protocol.MirrorSourceBinding{}, "", false
	}
	return *selected, catalog.DaemonGeneration, true
}

// CreateMirrorControlGrant issues a short-lived human input capability for one
// exact mirror source. Authorization uses the existing runtime availability
// gate (private = owner, public = workspace members); the host master switch
// must additionally be enabled.
func (h *Handler) CreateMirrorControlGrant(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "mirror_control_grant_create", runtimeID)
	if !ok {
		return
	}
	h.createOrRenewControlGrant(w, r, rt, false)
}

// RenewMirrorControlGrant revalidates the runtime gate and host switch before
// extending an existing capability.
func (h *Handler) RenewMirrorControlGrant(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "mirror_control_grant_renew", runtimeID)
	if !ok {
		return
	}
	h.createOrRenewControlGrant(w, r, rt, true)
}

func (h *Handler) createOrRenewControlGrant(w http.ResponseWriter, r *http.Request, rt db.AgentRuntime, isRenew bool) {
	if !rt.DaemonID.Valid || strings.TrimSpace(rt.DaemonID.String) == "" || rt.Status != "online" {
		writeVscreenReason(w, http.StatusConflict, "offline")
		return
	}
	if !runtimeHasCapability(rt.Metadata, protocol.DaemonCapabilityScreenControlV1) {
		writeVscreenReason(w, http.StatusNotImplemented, "upgrade_required")
		return
	}
	var req createControlGrantRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeVscreenReason(w, http.StatusBadRequest, "invalid_request")
		return
	}
	req.ViewerID = strings.TrimSpace(req.ViewerID)
	if req.ViewerID == "" || req.Source == nil || req.Source.Validate() != nil {
		writeVscreenReason(w, http.StatusBadRequest, "invalid_request")
		return
	}
	// Free human interaction mode targets physical/system displays. Virtual
	// display control runs through the task-bound intervention takeover flow.
	if req.Source.Kind == protocol.MirrorSourceVirtual {
		writeVscreenReason(w, http.StatusBadRequest, "virtual_interaction_requires_takeover")
		return
	}

	// The host master switch must be enabled; observe it from live state.
	state, err := h.DaemonHub.QueryVscreen(r.Context(), uuidToString(rt.WorkspaceID), uuidToString(rt.ID), rt.DaemonID.String, "state")
	if err != nil {
		writeVscreenError(w, err)
		return
	}
	if state.Reason != "" || state.State == nil || !state.State.HumanInteraction {
		writeVscreenReason(w, http.StatusConflict, "interaction_disabled")
		return
	}

	binding, generation, ok := h.resolveControlSource(w, r, rt, req)
	if !ok {
		return
	}
	viewer, ok := h.MirrorGrants.LookupViewer(uuidToString(rt.ID), req.ViewerID, requestUserID(r), time.Now())
	if !ok || !viewer.Active || viewer.DaemonID != rt.DaemonID.String || viewer.DaemonGeneration != generation ||
		viewer.Grant.Source != binding.Source || viewer.Grant.NativeEpoch != binding.NativeEpoch ||
		viewer.Grant.SourceGeneration != binding.Generation {
		writeVscreenReason(w, http.StatusConflict, "viewer_grant_required")
		return
	}

	now := time.Now()
	grant := protocol.MirrorControlGrant{
		GrantID:          uuid.NewString(),
		SessionID:        "control-" + req.ViewerID,
		WorkspaceID:      uuidToString(rt.WorkspaceID),
		RuntimeID:        uuidToString(rt.ID),
		UserID:           requestUserID(r),
		ViewerID:         req.ViewerID,
		NativeEpoch:      binding.NativeEpoch,
		Source:           binding.Source,
		SourceGeneration: binding.Generation,
		ExpiresAt:        now.Add(controlGrantTTL),
	}
	prior, priorExists := h.MirrorControlGrants.Lookup(grant.RuntimeID, grant.ViewerID, now)
	if priorExists {
		if prior.Grant.UserID != grant.UserID {
			writeVscreenReason(w, http.StatusForbidden, "permission_denied")
			return
		}
		if prior.Grant.NativeEpoch == grant.NativeEpoch &&
			prior.Grant.Source == grant.Source &&
			prior.Grant.SourceGeneration == grant.SourceGeneration {
			grant.GrantID = prior.Grant.GrantID
		}
	} else if isRenew {
		writeVscreenReason(w, http.StatusConflict, "control_grant_expired")
		return
	}
	if err := grant.Validate(now); err != nil {
		writeVscreenReason(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	record := mirror.ControlGrantRecord{
		Grant:            grant,
		DaemonID:         rt.DaemonID.String,
		DaemonGeneration: generation,
		CredentialHash:   viewer.CredentialHash,
		CredentialKind:   viewer.CredentialKind,
		CredentialExpiry: viewer.CredentialExpiry,
	}
	if err := h.MirrorControlGrants.Put(record, now); err != nil {
		writeError(w, http.StatusServiceUnavailable, "control grant unavailable")
		return
	}
	if !h.DaemonHub.SendControlGrant(rt.DaemonID.String, protocol.MirrorControlGrantPayload{
		WorkspaceID:      grant.WorkspaceID,
		RuntimeID:        grant.RuntimeID,
		DaemonGeneration: generation,
		Grant:            grant,
	}) {
		// The replacement event was never delivered, so the daemon still owns
		// the previous capability. Restore it server-side instead of revoking it.
		if h.MirrorControlGrants.RemoveIfCurrent(record); priorExists && prior.Grant.GrantID != grant.GrantID {
			if err := h.MirrorControlGrants.Put(prior, now); err != nil {
				h.DaemonHub.SendControlRevoke(prior.DaemonID, protocol.MirrorControlRevokePayload{
					WorkspaceID:      prior.Grant.WorkspaceID,
					RuntimeID:        prior.Grant.RuntimeID,
					DaemonGeneration: prior.DaemonGeneration,
					ViewerID:         prior.Grant.ViewerID,
					GrantID:          prior.Grant.GrantID,
				})
			}
		}
		writeVscreenReason(w, http.StatusServiceUnavailable, "daemon_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, grant)
}

func (h *Handler) validateControlCredential(ctx context.Context, record mirror.ControlGrantRecord) error {
	now := time.Now()
	if !record.Grant.ExpiresAt.After(now) ||
		(!record.CredentialExpiry.IsZero() && !record.CredentialExpiry.After(now)) {
		return mirror.ErrSessionExpired
	}
	viewerRecord := mirror.ViewerGrantRecord{
		Grant: protocol.MirrorViewerGrant{
			GrantID:          record.Grant.GrantID,
			SessionID:        record.Grant.SessionID,
			WorkspaceID:      record.Grant.WorkspaceID,
			RuntimeID:        record.Grant.RuntimeID,
			UserID:           record.Grant.UserID,
			ViewerID:         record.Grant.ViewerID,
			NativeEpoch:      record.Grant.NativeEpoch,
			Source:           record.Grant.Source,
			SourceGeneration: record.Grant.SourceGeneration,
			ExpiresAt:        record.Grant.ExpiresAt,
		},
		DaemonID:         record.DaemonID,
		DaemonGeneration: record.DaemonGeneration,
		CredentialHash:   record.CredentialHash,
		CredentialKind:   record.CredentialKind,
		CredentialExpiry: record.CredentialExpiry,
	}
	if err := h.refreshViewerCredential(ctx, &viewerRecord); err != nil {
		return err
	}
	catalog, err := h.DaemonHub.QueryVscreen(ctx, record.Grant.WorkspaceID, record.Grant.RuntimeID, record.DaemonID, "sources")
	if err != nil || catalog.DaemonGeneration != record.DaemonGeneration || catalog.Reason != "" {
		return mirror.ErrSessionIdentityMismatch
	}
	for _, source := range catalog.Sources {
		if source.Source == record.Grant.Source &&
			source.NativeEpoch == record.Grant.NativeEpoch &&
			source.Generation == record.Grant.SourceGeneration {
			return nil
		}
	}
	return mirror.ErrSessionIdentityMismatch
}

func (h *Handler) revokeControlGrant(record mirror.ControlGrantRecord) {
	current, ok := h.MirrorControlGrants.RemoveIfCurrent(record)
	if !ok {
		return
	}
	h.DaemonHub.SendControlRevoke(current.DaemonID, protocol.MirrorControlRevokePayload{
		WorkspaceID:      current.Grant.WorkspaceID,
		RuntimeID:        current.Grant.RuntimeID,
		DaemonGeneration: current.DaemonGeneration,
		ViewerID:         current.Grant.ViewerID,
		GrantID:          current.Grant.GrantID,
	})
}

func (h *Handler) revokeRuntimeControlGrants(rt db.AgentRuntime) {
	for _, record := range h.MirrorControlGrants.RemoveRuntime(rt.DaemonID.String, uuidToString(rt.ID)) {
		h.DaemonHub.SendControlRevoke(record.DaemonID, protocol.MirrorControlRevokePayload{
			WorkspaceID:      record.Grant.WorkspaceID,
			RuntimeID:        record.Grant.RuntimeID,
			DaemonGeneration: record.DaemonGeneration,
			ViewerID:         record.Grant.ViewerID,
			GrantID:          record.Grant.GrantID,
		})
	}
}

func (h *Handler) revokeControlGrantViewer(runtimeID, viewerID string) {
	record, ok := h.MirrorControlGrants.Lookup(runtimeID, viewerID, time.Now())
	if ok {
		h.revokeControlGrant(record)
	}
}

func (h *Handler) revokeControlGrantsForDaemon(daemonID string, runtimeIDs []string) {
	for _, record := range h.MirrorControlGrants.RemoveDaemon(daemonID, runtimeIDs) {
		h.DaemonHub.SendControlRevoke(record.DaemonID, protocol.MirrorControlRevokePayload{
			WorkspaceID:      record.Grant.WorkspaceID,
			RuntimeID:        record.Grant.RuntimeID,
			DaemonGeneration: record.DaemonGeneration,
			ViewerID:         record.Grant.ViewerID,
			GrantID:          record.Grant.GrantID,
		})
	}
}

func (h *Handler) sweepControlGrants(ctx context.Context) {
	now := time.Now()
	for _, record := range h.MirrorControlGrants.Sweep(now) {
		h.DaemonHub.SendControlRevoke(record.DaemonID, protocol.MirrorControlRevokePayload{
			WorkspaceID:      record.Grant.WorkspaceID,
			RuntimeID:        record.Grant.RuntimeID,
			DaemonGeneration: record.DaemonGeneration,
			ViewerID:         record.Grant.ViewerID,
			GrantID:          record.Grant.GrantID,
		})
	}
	for _, record := range h.MirrorControlGrants.Records() {
		if ctx.Err() != nil {
			return
		}
		if err := h.validateControlCredential(ctx, record); err != nil {
			h.revokeControlGrant(record)
		}
	}
}

// RevokeMirrorControlGrant returns a viewer to read-only immediately.
func (h *Handler) RevokeMirrorControlGrant(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "mirror_control_grant_revoke", runtimeID)
	if !ok {
		return
	}
	viewerID := strings.TrimSpace(chi.URLParam(r, "viewerId"))
	if viewerID == "" {
		writeVscreenReason(w, http.StatusBadRequest, "invalid_request")
		return
	}
	record, exists := h.MirrorControlGrants.Lookup(uuidToString(rt.ID), viewerID, time.Now())
	if exists {
		if record.Grant.UserID != requestUserID(r) && !canSetRuntimeVisibility(member, rt) {
			writeVscreenReason(w, http.StatusForbidden, "permission_denied")
			return
		}
		h.revokeControlGrant(record)
	}
	w.WriteHeader(http.StatusNoContent)
}
