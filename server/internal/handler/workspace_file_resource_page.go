package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) addWorkspaceFileIdentities(ctx context.Context, directory workspaceFileDirectory, action, relativePath string, result *protocol.LocalReviewResult) error {
	var page map[string]json.RawMessage
	if err := json.Unmarshal(result.Page, &page); err != nil || page == nil {
		return errInvalidWorkspaceFileObservation
	}
	var entries []map[string]json.RawMessage
	var observations []workspaceFileObservation
	switch action {
	case "workspace_files":
		var root string
		if json.Unmarshal(page["path"], &root) != nil || root != directory.binding.WorkDir || json.Unmarshal(page["entries"], &entries) != nil || len(entries) > 5000 {
			return errInvalidWorkspaceFileObservation
		}
		for _, entry := range entries {
			var observed workspaceFileObservation
			if json.Unmarshal(entry["path"], &observed.Path) != nil || json.Unmarshal(entry["directory"], &observed.Directory) != nil {
				return errInvalidWorkspaceFileObservation
			}
			observations = append(observations, observed)
		}
	case "file_content":
		var selected string
		if json.Unmarshal(page["path"], &selected) != nil || selected != relativePath {
			return errInvalidWorkspaceFileObservation
		}
		observations = []workspaceFileObservation{{Path: relativePath}}
	default:
		return fmt.Errorf("%w: unsupported observation action", errInvalidWorkspaceFileObservation)
	}
	resources, err := h.observeWorkspaceFiles(ctx, directory, observations, true)
	if err != nil {
		return err
	}
	directoryID, err := json.Marshal(directory.key)
	if err != nil {
		return fmt.Errorf("encode workspace directory identity: %w", err)
	}
	page["directory_id"] = directoryID
	if directory.readRevision != nil {
		page["directory_revision"], err = json.Marshal(*directory.readRevision)
		if err != nil {
			return err
		}
	}
	if action == "workspace_files" {
		for _, entry := range entries {
			var selected string
			if err := json.Unmarshal(entry["path"], &selected); err != nil {
				return fmt.Errorf("read workspace file location: %w", err)
			}
			delete(entry, "file_id")
			if resource, ok := resources[selected]; ok {
				entry["file_id"], err = json.Marshal(uuidToString(resource.ID))
				if err != nil {
					return fmt.Errorf("encode workspace file identity: %w", err)
				}
			}
		}
		page["entries"], err = json.Marshal(entries)
	} else {
		delete(page, "file_id")
		page["file_identity_ambiguous"], err = json.Marshal(directory.identityAmbiguous)
		if err != nil {
			return err
		}
		if resource, ok := resources[relativePath]; ok {
			page["file_id"], err = json.Marshal(uuidToString(resource.ID))
		}
	}
	if err != nil {
		return fmt.Errorf("encode identified workspace files: %w", err)
	}
	result.Page, err = json.Marshal(page)
	return err
}
