package middleware

import (
	"net/http"
	"slices"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func authenticateExecutionGrant(w http.ResponseWriter, r *http.Request, q *db.Queries, token string, next http.Handler) {
	// Match literal segments, not substrings or route suffixes. This credential
	// must never authenticate runtime management or another account API.
	taskID, operation, _, allowed := protocol.ExecutionCallbackOperation(r.Method, r.URL.EscapedPath())
	if !allowed || r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, http.StatusForbidden, "execution grant does not authorize this operation")
		return
	}
	if q == nil || len(token) != 68 {
		writeError(w, http.StatusUnauthorized, "invalid execution grant")
		return
	}
	hash := auth.HashToken(token)
	grant, err := q.GetExecutionGrant(r.Context(), hash)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid execution grant")
		return
	}
	if taskID != util.UUIDToString(grant.TaskID) || !slices.Contains(grant.Operations, operation) {
		writeError(w, http.StatusForbidden, "execution grant scope mismatch")
		return
	}
	ctx := WithDaemonContext(r.Context(), util.UUIDToString(grant.WorkspaceID), grant.DaemonID)
	ctx = auth.WithExecutionRequest(ctx, auth.ExecutionRequest{Present: true, ExecutionID: util.UUIDToString(grant.ExecutionID), WorkerID: util.UUIDToString(grant.WorkerID), GrantHash: hash, Operation: operation})
	next.ServeHTTP(w, r.WithContext(ctx))
}
