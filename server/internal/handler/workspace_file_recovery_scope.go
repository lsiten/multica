package handler

import (
	"encoding/json"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func workspaceFileRecoveryScope(original db.WorkspaceFileOperation, directory workspaceFileDirectory) (protocol.WorkspaceFileOperation, []workspaceFileSnapshot, error) {
	var deleted protocol.WorkspaceFileOperation
	var resources []workspaceFileSnapshot
	if original.State != "completed" || original.ActorID != directory.runtime.OwnerID || original.PhysicalKey != directory.physicalKey {
		return deleted, resources, errWorkspaceFileRecoveryUnavailable
	}
	if json.Unmarshal(original.Operation, &deleted) != nil || deleted.Kind != protocol.WorkspaceTrash || deleted.Validate() != nil || json.Unmarshal(original.Resources, &resources) != nil {
		return deleted, resources, errWorkspaceFileRecoveryUnavailable
	}
	if original.DirectoryKey == directory.key {
		return deleted, resources, nil
	}
	for _, resource := range resources {
		if resource.Side == "source" && resource.DirectoryKey == directory.key {
			return deleted, resources, nil
		}
	}
	return deleted, resources, errWorkspaceFileRecoveryUnavailable
}
