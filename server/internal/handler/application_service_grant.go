package handler

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const applicationServiceGrantTTL = 15 * time.Minute

func applicationServiceGrantState(authority db.ApplicationServiceAuthority) protocol.ApplicationServiceGrantState {
	return protocol.ApplicationServiceGrantState{Capability: protocol.ApplicationServiceGrantCapability, WorkspaceID: uuidToString(authority.WorkspaceID), RuntimeID: uuidToString(authority.RuntimeID), DaemonID: authority.DaemonID, ServiceInstanceID: uuidToString(authority.ServiceInstanceID), Generation: authority.Generation, Revoked: authority.Revoked}
}

func (h *Handler) beginApplicationServiceParent(w http.ResponseWriter, r *http.Request, runtime db.AgentRuntime) (pgx.Tx, *db.Queries, db.LockApplicationServiceRuntimeOwnerRow, bool) {
	if !protocol.ValidApplicationDaemonID(runtime.DaemonID.String) {
		writeError(w, 400, "invalid daemon_id")
		return nil, nil, db.LockApplicationServiceRuntimeOwnerRow{}, false
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		h.applicationError(w, err)
		return nil, nil, db.LockApplicationServiceRuntimeOwnerRow{}, false
	}
	q := h.Queries.WithTx(tx)
	if _, err = q.LockApplicationWorkspace(r.Context(), runtime.WorkspaceID); err != nil {
		tx.Rollback(r.Context())
		h.applicationError(w, err)
		return nil, nil, db.LockApplicationServiceRuntimeOwnerRow{}, false
	}
	owner, err := q.LockApplicationServiceRuntimeOwner(r.Context(), db.LockApplicationServiceRuntimeOwnerParams{RuntimeID: runtime.ID, WorkspaceID: runtime.WorkspaceID, DaemonID: runtime.DaemonID})
	if err != nil || middleware.DaemonIDFromContext(r.Context()) == "" && requestUserID(r) != uuidToString(owner.OwnerID) {
		tx.Rollback(r.Context())
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			h.applicationError(w, err)
		} else {
			writeError(w, http.StatusForbidden, "application runtime ownership changed")
		}
		return nil, nil, db.LockApplicationServiceRuntimeOwnerRow{}, false
	}
	return tx, q, owner, true
}

// GetApplicationServiceGrantState exposes authority metadata without recovering secrets.
func (h *Handler) GetApplicationServiceGrantState(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.requireExecutionRuntime(w, r)
	if !ok {
		return
	}
	tx, q, _, ok := h.beginApplicationServiceParent(w, r, runtime)
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())
	state := protocol.ApplicationServiceGrantState{Capability: protocol.ApplicationServiceGrantCapability, RuntimeID: uuidToString(runtime.ID), WorkspaceID: uuidToString(runtime.WorkspaceID), DaemonID: runtime.DaemonID.String}
	authority, err := q.GetApplicationServiceAuthority(r.Context(), runtime.ID)
	if err == nil {
		state = applicationServiceGrantState(authority)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		h.applicationError(w, err)
		return
	}
	if !commitExecutionCallback(w, r, tx) {
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// IssueApplicationServiceGrant advances authority on every renewal or replacement.
// Old credentials and sockets are not grandfathered across rotation.
func (h *Handler) IssueApplicationServiceGrant(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.requireExecutionRuntime(w, r)
	if !ok {
		return
	}
	var input protocol.ApplicationServiceGrantRequest
	if !decodeApplicationRequest(w, r, &input) {
		return
	}
	instance, ok := parseUUIDOrBadRequest(w, input.ServiceInstanceID, "service_instance_id")
	if !ok {
		return
	}
	if instance.Bytes == [16]byte{} || input.ExpectedGeneration < 0 || len(input.Operations) == 0 || len(input.Operations) > 7 {
		writeError(w, 400, "invalid application service grant scope")
		return
	}
	for _, operation := range input.Operations {
		if !protocol.ApplicationServiceOperationAllowed(operation) {
			writeError(w, 400, "unsupported application service operation")
			return
		}
	}
	slices.Sort(input.Operations)
	input.Operations = slices.Compact(input.Operations)
	tx, q, owner, ok := h.beginApplicationServiceParent(w, r, runtime)
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())
	var authority db.ApplicationServiceAuthority
	var err error
	if input.ExpectedGeneration == 0 {
		authority, err = q.CreateApplicationServiceAuthority(r.Context(), db.CreateApplicationServiceAuthorityParams{RuntimeID: runtime.ID, WorkspaceID: runtime.WorkspaceID, DaemonID: runtime.DaemonID.String, OwnerID: owner.OwnerID, MemberID: owner.MemberID, ServiceInstanceID: instance})
	} else {
		authority, err = q.ReplaceApplicationServiceAuthority(r.Context(), db.ReplaceApplicationServiceAuthorityParams{RuntimeID: runtime.ID, WorkspaceID: runtime.WorkspaceID, DaemonID: runtime.DaemonID.String, OwnerID: owner.OwnerID, MemberID: owner.MemberID, ServiceInstanceID: instance, ExpectedGeneration: input.ExpectedGeneration})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "application service authority changed; query current generation")
		return
	}
	if err != nil {
		h.applicationError(w, err)
		return
	}
	token, err := auth.GenerateApplicationServiceGrant()
	if err != nil {
		h.applicationError(w, err)
		return
	}
	expires := time.Now().UTC().Add(applicationServiceGrantTTL)
	if err = q.CreateApplicationServiceGrant(r.Context(), db.CreateApplicationServiceGrantParams{TokenHash: auth.HashToken(token), RuntimeID: runtime.ID, Generation: authority.Generation, Operations: input.Operations, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}}); err != nil {
		h.applicationError(w, err)
		return
	}
	if !commitExecutionCallback(w, r, tx) {
		return
	}
	writeJSON(w, http.StatusOK, protocol.ApplicationServiceGrantResponse{ApplicationServiceGrantState: applicationServiceGrantState(authority), Token: token, Operations: input.Operations, ExpiresAt: expires})
}

// RevokeApplicationServiceGrant is idempotent for the named generation only.
func (h *Handler) RevokeApplicationServiceGrant(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.requireExecutionRuntime(w, r)
	if !ok {
		return
	}
	var input protocol.RevokeApplicationServiceGrantRequest
	if !decodeApplicationRequest(w, r, &input) {
		return
	}
	instance, ok := parseUUIDOrBadRequest(w, input.ServiceInstanceID, "service_instance_id")
	if !ok {
		return
	}
	if input.Generation < 1 {
		writeError(w, 400, "invalid application service generation")
		return
	}
	tx, q, _, ok := h.beginApplicationServiceParent(w, r, runtime)
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())
	_, err := q.RevokeApplicationServiceAuthority(r.Context(), db.RevokeApplicationServiceAuthorityParams{RuntimeID: runtime.ID, WorkspaceID: runtime.WorkspaceID, DaemonID: runtime.DaemonID.String, ServiceInstanceID: instance, Generation: input.Generation})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "application service authority changed")
		return
	}
	if err != nil {
		h.applicationError(w, err)
		return
	}
	if !commitExecutionCallback(w, r, tx) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
