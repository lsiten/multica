package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const applicationServiceRevalidationInterval = time.Second

func (h *Handler) authorizeApplicationServiceSocket(w http.ResponseWriter, r *http.Request, runtime db.AgentRuntime) bool {
	identity := auth.ApplicationServiceIdentityFromContext(r.Context())
	if identity.TokenHash == "" {
		return true
	}
	grant, err := h.Queries.GetApplicationServiceGrant(r.Context(), identity.TokenHash)
	if err != nil || !applicationServiceGrantMatches(identity, grant) || uuidToString(runtime.ID) != identity.RuntimeID || uuidToString(runtime.WorkspaceID) != identity.WorkspaceID {
		writeApplicationServiceCredentialError(w, err)
		return false
	}
	return true
}

// applicationServiceSocket owns its watcher until Control or scoped Attach
// returns. Expiry is a deadline, and live authority is rechecked without a cache
// at most every second; an unavailable authority fails closed.
func (h *Handler) applicationServiceSocket(r *http.Request, connection *websocket.Conn) (context.Context, func()) {
	identity := auth.ApplicationServiceIdentityFromContext(r.Context())
	if identity.TokenHash == "" {
		return r.Context(), func() {}
	}
	ctx, cancel := context.WithDeadline(r.Context(), identity.ExpiresAt)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(applicationServiceRevalidationInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				connection.Close()
				return
			case <-ticker.C:
				queryCtx, stop := context.WithTimeout(ctx, time.Second)
				grant, err := h.Queries.GetApplicationServiceGrant(queryCtx, identity.TokenHash)
				stop()
				if err != nil || !applicationServiceGrantMatches(identity, grant) {
					cancel()
					connection.Close()
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(); <-done }
}
