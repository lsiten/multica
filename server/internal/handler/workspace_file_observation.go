package handler

import (
	"context"
	"encoding/json"
	"fmt"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func observeWorkspaceFilesInTransaction(ctx context.Context, q *db.Queries, directory workspaceFileDirectory, entries []workspaceFileObservation, observed bool) (map[string]db.WorkspaceFileResource, error) {
	if len(entries) > 5000 {
		return nil, errInvalidWorkspaceFileObservation
	}
	unique := make(map[string]workspaceFileObservation, len(entries))
	for _, entry := range entries {
		if !protocol.ValidWorkspaceOperationPath(entry.Path) {
			continue
		}
		if previous, ok := unique[entry.Path]; ok && previous.Directory != entry.Directory {
			return nil, errInvalidWorkspaceFileObservation
		}
		entry.PathKey = workspaceFileDigest(entry.Path)
		unique[entry.Path] = entry
	}
	resources := make(map[string]db.WorkspaceFileResource, len(unique))
	if len(unique) == 0 {
		return resources, nil
	}
	batch := make([]workspaceFileObservation, 0, len(unique))
	for _, entry := range unique {
		batch = append(batch, entry)
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		return nil, fmt.Errorf("encode workspace file observations: %w", err)
	}
	binding, err := json.Marshal(directory.binding)
	if err != nil {
		return nil, fmt.Errorf("encode workspace file binding: %w", err)
	}
	if err := lockDocumentCommentOwner(ctx, q, directory.workspaceID, directory.binding); err != nil {
		return nil, fmt.Errorf("lock workspace file owner: %w", err)
	}
	rows, err := q.ObserveWorkspaceFileResources(ctx, db.ObserveWorkspaceFileResourcesParams{WorkspaceID: directory.workspaceID, DirectoryKey: directory.key, BindingRef: binding, Entries: payload, Observed: observed})
	if err != nil {
		return nil, fmt.Errorf("observe workspace files: %w", err)
	}
	for _, row := range rows {
		resources[row.RelativePath] = row
	}
	return resources, nil
}
