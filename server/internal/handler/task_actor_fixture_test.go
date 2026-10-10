package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// legacyTaskActorRequest supplies an actual legacy token row to older direct
// handler fixtures. Header-only requests are deliberately no longer credentials.
func legacyTaskActorRequest(t *testing.T, request *http.Request) *http.Request {
	t.Helper()
	token, err := testHandler.Queries.CreateTaskToken(t.Context(), db.CreateTaskTokenParams{TokenHash: "fixture-actor-" + uuid.NewString(), TaskID: parseUUID(request.Header.Get("X-Task-ID")), AgentID: parseUUID(request.Header.Get("X-Agent-ID")), WorkspaceID: parseUUID(request.Header.Get("X-Workspace-ID")), UserID: parseUUID(request.Header.Get("X-User-ID")), ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Cleanup(t, "DELETE FROM task_token WHERE id=$1", token.ID)
	return request.WithContext(auth.WithTaskActor(request.Context(), auth.TaskActor{TokenID: uuidToString(token.ID), TokenHash: token.TokenHash, TaskID: uuidToString(token.TaskID), AgentID: uuidToString(token.AgentID), WorkspaceID: uuidToString(token.WorkspaceID), UserID: uuidToString(token.UserID)}))
}
