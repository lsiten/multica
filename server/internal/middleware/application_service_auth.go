package middleware

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func applicationServiceOperation(r *http.Request) (string, string, bool) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 6 || parts[0] != "api" || parts[1] != "daemon" || parts[2] != "runtimes" || parts[4] != "applications" || uuid.Validate(parts[3]) != nil {
		return "", "", false
	}
	if r.Method == http.MethodPost {
		if len(parts) == 6 && (parts[5] == "sync" || parts[5] == "claim" || parts[5] == "observe") {
			return parts[3], parts[5], true
		}
		if len(parts) == 8 && parts[5] == "steps" && uuid.Validate(parts[6]) == nil && (parts[7] == "result" || parts[7] == "lease") {
			return parts[3], parts[7], true
		}
	}
	if r.Method == http.MethodGet && len(parts) == 7 && parts[5] == "tunnel" && (parts[6] == "control" || parts[6] == "data") {
		return parts[3], "tunnel_" + parts[6], true
	}
	return "", "", false
}

func authenticateApplicationServiceGrant(w http.ResponseWriter, r *http.Request, q *db.Queries, token string, next http.Handler) {
	runtimeID, operation, ok := applicationServiceOperation(r)
	if !ok {
		writeError(w, http.StatusForbidden, "application service grant does not authorize this operation")
		return
	}
	if q == nil || len(token) != 68 {
		writeError(w, http.StatusUnauthorized, "invalid application service grant")
		return
	}
	tokenHash := auth.HashToken(token)
	grant, err := q.GetApplicationServiceGrant(r.Context(), tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "invalid application service grant")
		} else {
			writeError(w, http.StatusServiceUnavailable, "application service authorization unavailable")
		}
		return
	}
	if util.UUIDToString(grant.RuntimeID) != runtimeID || !slices.Contains(grant.Operations, operation) {
		writeError(w, http.StatusForbidden, "application service grant scope mismatch")
		return
	}
	identity := auth.ApplicationServiceIdentity{TokenHash: tokenHash, RuntimeID: runtimeID, WorkspaceID: util.UUIDToString(grant.WorkspaceID), DaemonID: grant.DaemonID, OwnerID: util.UUIDToString(grant.OwnerID), MemberID: util.UUIDToString(grant.MemberID), ServiceInstanceID: util.UUIDToString(grant.ServiceInstanceID), Generation: grant.Generation, Operation: operation, ExpiresAt: grant.ExpiresAt.Time}
	ctx := WithDaemonContext(r.Context(), identity.WorkspaceID, identity.DaemonID)
	next.ServeHTTP(w, r.WithContext(auth.WithApplicationServiceIdentity(ctx, identity)))
}
