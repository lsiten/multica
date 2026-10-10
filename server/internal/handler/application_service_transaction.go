package handler

import (
	"errors"
	"net/http"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type applicationServiceTransaction struct {
	service  *application.Service
	tx       pgx.Tx
	queries  *db.Queries
	identity auth.ApplicationServiceIdentity
}

func applicationServiceGrantMatches(identity auth.ApplicationServiceIdentity, grant db.GetApplicationServiceGrantRow) bool {
	return grant.TokenHash == identity.TokenHash && uuidToString(grant.RuntimeID) == identity.RuntimeID && uuidToString(grant.WorkspaceID) == identity.WorkspaceID && grant.DaemonID == identity.DaemonID && uuidToString(grant.OwnerID) == identity.OwnerID && uuidToString(grant.MemberID) == identity.MemberID && uuidToString(grant.ServiceInstanceID) == identity.ServiceInstanceID && grant.Generation == identity.Generation && slices.Contains(grant.Operations, identity.Operation)
}

// beginApplicationServiceTransaction adds a grant fence to the existing service
// transaction. pgx nested transactions are savepoints on this same connection,
// so claim/generation checks and grant revocation cannot race separate commits.
func (h *Handler) beginApplicationServiceTransaction(w http.ResponseWriter, r *http.Request, runtime db.AgentRuntime) (*applicationServiceTransaction, bool) {
	identity := auth.ApplicationServiceIdentityFromContext(r.Context())
	if identity.TokenHash == "" {
		return &applicationServiceTransaction{service: h.applicationService()}, true
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		h.applicationError(w, err)
		return nil, false
	}
	q := h.Queries.WithTx(tx)
	if _, err = q.LockApplicationWorkspace(r.Context(), runtime.WorkspaceID); err != nil {
		tx.Rollback(r.Context())
		h.applicationError(w, err)
		return nil, false
	}
	owner, err := q.LockApplicationServiceRuntimeOwner(r.Context(), db.LockApplicationServiceRuntimeOwnerParams{RuntimeID: runtime.ID, WorkspaceID: runtime.WorkspaceID, DaemonID: runtime.DaemonID})
	if err != nil || uuidToString(owner.OwnerID) != identity.OwnerID || uuidToString(owner.MemberID) != identity.MemberID {
		tx.Rollback(r.Context())
		writeApplicationServiceCredentialError(w, err)
		return nil, false
	}
	grant, err := q.LockApplicationServiceGrant(r.Context(), identity.TokenHash)
	if err != nil || !applicationServiceGrantMatches(identity, db.GetApplicationServiceGrantRow(grant)) {
		tx.Rollback(r.Context())
		writeApplicationServiceCredentialError(w, err)
		return nil, false
	}
	return &applicationServiceTransaction{service: &application.Service{Queries: q, Transactions: tx}, tx: tx, queries: q, identity: identity}, true
}

func (s *applicationServiceTransaction) rollback(r *http.Request) {
	if s.tx != nil {
		s.tx.Rollback(r.Context())
	}
}

func (s *applicationServiceTransaction) commit(w http.ResponseWriter, r *http.Request) bool {
	if s.tx == nil {
		return true
	}
	// clock_timestamp() in this read checks expiry at the actual commit boundary,
	// not at the outer transaction's start time.
	grant, err := s.queries.GetApplicationServiceGrant(r.Context(), s.identity.TokenHash)
	if err != nil || !applicationServiceGrantMatches(s.identity, grant) {
		writeApplicationServiceCredentialError(w, err)
		return false
	}
	if err = s.tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "commit application service request")
		return false
	}
	return true
}

func writeApplicationServiceCredentialError(w http.ResponseWriter, err error) {
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusServiceUnavailable, "application service authorization unavailable")
		return
	}
	writeError(w, http.StatusUnauthorized, "application service grant expired or revoked")
}
