package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (s *environmentProcessService) environmentCommandRead(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	var input struct {
		WorkspaceID string                      `json:"workspace_id"`
		RuntimeID   string                      `json:"runtime_id"`
		Command     protocol.EnvironmentCommand `json:"command"`
	}
	if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
		return environmentOperationResult(nil, err)
	}
	switch input.Command.Action {
	case "inventory", "cache_preview", "cleanup_preview", "archive_preview":
	default:
		return environmentOperationResult(nil, errors.New("environment read requires a preview action"))
	}
	result, err := s.daemon.executeEnvironmentCommand(ctx, environmentOperationScope{WorkspaceID: input.WorkspaceID, RuntimeID: input.RuntimeID}, input.Command)
	return environmentOperationResult(result, err)
}
