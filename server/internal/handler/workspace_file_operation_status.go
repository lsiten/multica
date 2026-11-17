package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// tryServeWorkspaceFileOperationStatus resolves a command from the durable
// actor-owned operation ledger before a retry attempts to load a runtime route.
// A completed receipt is safe to replay; a pending dispatched command is kept
// protected and never sent to another provider.
func (h *Handler) tryServeWorkspaceFileOperationStatus(ctx context.Context, w http.ResponseWriter, workspaceID, actorID, commandID string, operation *protocol.WorkspaceFileOperation) (bool, error) {
	if commandID == "" || operation == nil {
		return false, nil
	}
	rows, err := h.Queries.ListWorkspaceFileOperationsByActorCommand(ctx, db.ListWorkspaceFileOperationsByActorCommandParams{
		WorkspaceID: parseUUID(workspaceID), ActorID: parseUUID(actorID), CommandID: commandID,
	})
	if errors.Is(err, pgx.ErrNoRows) || len(rows) == 0 {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(rows) != 1 {
		return true, errWorkspaceFileCommandMismatch
	}
	record := rows[0]
	payload, err := json.Marshal(*operation)
	if err != nil {
		return true, err
	}
	if workspaceFileDigest(string(payload)) != record.RequestHash {
		return true, errWorkspaceFileCommandMismatch
	}
	if record.State == "pending" {
		return true, errWorkspaceFileOperationUnknown
	}
	result, err := completedWorkspaceFileResult(record)
	if err != nil {
		return true, err
	}
	writeWorkspaceFileOperationResult(w, result)
	return true, nil
}
