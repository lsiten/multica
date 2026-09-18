package handler

import (
	"context"
	"errors"
	"time"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) validateViewerCredential(ctx context.Context, record mirror.ViewerGrantRecord) error {
	return h.refreshViewerCredential(ctx, &record)
}

func (h *Handler) refreshViewerCredential(ctx context.Context, record *mirror.ViewerGrantRecord) error {
	if !record.CredentialExpiry.IsZero() && !record.CredentialExpiry.After(time.Now()) {
		return mirror.ErrSessionExpired
	}
	userID, err := util.ParseUUID(record.Grant.UserID)
	if err != nil {
		return err
	}
	user, err := h.Queries.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if auth.IsTemporarilyDisabledUser(record.Grant.UserID, user.Email) {
		return mirror.ErrSessionIdentityMismatch
	}
	switch record.CredentialKind {
	case "jwt":
		if record.CredentialExpiry.IsZero() {
			return mirror.ErrSessionExpired
		}
	case "pat":
		pat, err := h.Queries.GetPersonalAccessTokenByHash(ctx, record.CredentialHash)
		if err != nil {
			return err
		}
		if pat.ExpiresAt.Valid && (record.CredentialExpiry.IsZero() || pat.ExpiresAt.Time.Before(record.CredentialExpiry)) {
			record.CredentialExpiry = pat.ExpiresAt.Time
		}
		if !record.CredentialExpiry.IsZero() && !record.CredentialExpiry.After(time.Now()) {
			return mirror.ErrSessionExpired
		}
		if pat.UserID != userID {
			return mirror.ErrSessionIdentityMismatch
		}
	default:
		return mirror.ErrSessionIdentityMismatch
	}
	workspaceID, err := util.ParseUUID(record.Grant.WorkspaceID)
	if err != nil {
		return err
	}
	runtimeID, err := util.ParseUUID(record.Grant.RuntimeID)
	if err != nil {
		return err
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: userID, WorkspaceID: workspaceID})
	if err != nil {
		return err
	}
	rt, err := h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{ID: runtimeID, WorkspaceID: workspaceID})
	if err != nil {
		return err
	}
	if !canUseRuntimeForAgent(member, rt) || !rt.DaemonID.Valid || rt.DaemonID.String != record.DaemonID || rt.Status != "online" {
		return mirror.ErrSessionIdentityMismatch
	}
	return nil
}

func (h *Handler) renewViewerGrant(ctx context.Context, record mirror.ViewerGrantRecord) (mirror.ViewerGrantRecord, error) {
	if err := h.refreshViewerCredential(ctx, &record); err != nil {
		return mirror.ViewerGrantRecord{}, err
	}
	catalog, err := h.DaemonHub.QueryVscreen(ctx, record.Grant.WorkspaceID, record.Grant.RuntimeID, record.DaemonID, "sources")
	if err != nil {
		return mirror.ViewerGrantRecord{}, err
	}
	if catalog.DaemonGeneration != record.DaemonGeneration || catalog.Reason != "" {
		return mirror.ViewerGrantRecord{}, mirror.ErrSessionIdentityMismatch
	}
	found := false
	for _, source := range catalog.Sources {
		if source.Source == record.Grant.Source && source.Generation == record.Grant.SourceGeneration && source.NativeEpoch == record.Grant.NativeEpoch && (!record.Legacy || source.Primary) {
			found = true
			break
		}
	}
	if !found {
		return mirror.ViewerGrantRecord{}, mirror.ErrSessionIdentityMismatch
	}
	now := time.Now()
	expiry := now.Add(30 * time.Second)
	if !record.CredentialExpiry.IsZero() && record.CredentialExpiry.Before(expiry) {
		expiry = record.CredentialExpiry
	}
	renewed, err := h.MirrorGrants.Renew(record, expiry, now)
	if err != nil {
		return mirror.ViewerGrantRecord{}, err
	}
	if !h.DaemonHub.SendViewerRenew(record.DaemonID, protocol.MirrorViewerRenewPayload{DaemonGeneration: record.DaemonGeneration, Grant: renewed.Grant}) {
		h.revokeViewerGrant(renewed)
		return mirror.ViewerGrantRecord{}, mirror.ErrSessionClosed
	}
	return renewed, nil
}

// RunViewerGrantLoop revalidates active leases independently of the SDP session janitor.
func (h *Handler) RunViewerGrantLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.sweepViewerGrants(ctx)
		}
	}
}

func (h *Handler) sweepViewerGrants(ctx context.Context) {
	for _, record := range h.MirrorGrants.Records() {
		if ctx.Err() != nil {
			return
		}
		if !record.Grant.ExpiresAt.After(time.Now()) {
			h.revokeViewerGrant(record)
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := h.validateViewerCredential(checkCtx, record)
		if err == nil && record.Legacy && record.Active {
			_, err = h.renewViewerGrant(checkCtx, record)
		}
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			h.revokeViewerGrant(record)
		}
	}
}
