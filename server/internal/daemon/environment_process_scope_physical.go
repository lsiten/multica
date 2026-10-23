package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func (scope *environmentMutationScope) addWorkline(provenance execenv.ManagedEnvProvenance) {
	provenance.AgentName, provenance.ManagedBy = "", ""
	if provenance.CodeWorkspaceIssueID != "" {
		provenance.IssueID = provenance.CodeWorkspaceIssueID
		provenance.AgentID, provenance.SquadID, provenance.ChatSessionID, provenance.AutopilotID = "", "", "", ""
	}
	raw, err := json.Marshal(provenance)
	if err != nil {
		scope.Global = true
		return
	}
	digest := sha256.Sum256(raw)
	scope.addKey("workline:" + hex.EncodeToString(digest[:]))
}

func (s *environmentProcessService) preparationMutationScope(id string) environmentMutationScope {
	s.mu.RLock()
	owner := s.physical
	s.mu.RUnlock()
	if owner == nil {
		return environmentMutationScope{Global: true}
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	preparation := owner.preparations[id]
	if preparation == nil {
		return environmentMutationScope{Global: true}
	}
	result := preparation.Result
	scope := environmentMutationScope{WorkspaceID: result.Reservation.WorkspaceID}
	scope.addKey("task:" + scope.WorkspaceID + ":" + result.Reservation.TaskID)
	for _, path := range []string{result.Reservation.RootDir, result.CodeRoot, result.WorkDir, result.WorktreePath, result.GitRoot} {
		scope.addPath(path)
	}
	if preparation.Physical != nil {
		physical := preparation.Physical
		for _, path := range []string{physical.RootDir, physical.CodeRootDir, physical.WorkDir} {
			scope.addPath(path)
		}
		if physical.LocalWorktree != nil {
			scope.addPath(physical.LocalWorktree.GitRoot)
			scope.addPath(physical.LocalWorktree.Path)
		}
	}
	for _, root := range []string{result.Reservation.RootDir, result.CodeRoot} {
		if root != "" {
			scope.addDirectoryBindings(root)
		}
	}
	if len(scope.Paths) == 0 {
		scope.Global = true
	}
	return scope
}

func (s *environmentProcessService) prepareMutationScope(ctx context.Context, input execenv.PhysicalPrepareParams) environmentMutationScope {
	scope := environmentMutationScope{WorkspaceID: input.WorkspaceID}
	scope.addKey("task:" + input.WorkspaceID + ":" + input.TaskID)
	provenance, err := managedScopeForTask(physicalTaskForSelection(environmentPhysicalSelection{Physical: input}))
	if err != nil {
		scope.Global = true
	} else {
		scope.addWorkline(provenance)
	}
	for _, path := range []string{input.LocalWorkDir, input.CodeWorkspaceWorkDir} {
		scope.addPath(path)
	}
	if input.LocalWorktree != nil {
		scope.addPath(input.LocalWorktree.LocalPath)
	}
	// Existing task roots can carry a different family checkout from this request.
	s.mu.RLock()
	owner := s.physical
	s.mu.RUnlock()
	if owner != nil {
		owner.mu.Lock()
		ids := []string{}
		for id, preparation := range owner.preparations {
			reservation := preparation.Result.Reservation
			if reservation.WorkspaceID == input.WorkspaceID && reservation.TaskID == input.TaskID {
				ids = append(ids, id)
			}
		}
		owner.mu.Unlock()
		for _, id := range ids {
			scope.merge(s.preparationMutationScope(id))
		}
	}
	if s.daemon != nil {
		roots, scanErr := s.daemon.environmentRootPaths(ctx)
		if scanErr != nil {
			scope.Global = true
		} else {
			task := physicalTaskForSelection(environmentPhysicalSelection{Physical: input})
			for _, root := range roots {
				owner, ownerErr := s.daemon.gcTaskDirOwner(root)
				if ownerErr != nil {
					continue
				}
				sameTask := owner.WorkspaceID == input.WorkspaceID && owner.TaskID == input.TaskID
				provenance, provenanceErr := execenv.ReadManagedEnvProvenance(root)
				sameWorkline := provenanceErr == nil && managedReuseScopeMatches(task, provenance)
				if sameTask || sameWorkline {
					scope.addPath(root)
					scope.addDirectoryBindings(root)
				}
			}
		}
	}
	if input.WorkspaceID == "" || input.TaskID == "" {
		scope.broaden()
	}
	return scope
}

func (scope *environmentMutationScope) merge(other environmentMutationScope) {
	scope.Global = scope.Global || other.Global
	scope.WorkspaceWide = scope.WorkspaceWide || other.WorkspaceWide
	if scope.WorkspaceID == "" {
		scope.WorkspaceID = other.WorkspaceID
	}
	if other.WorkspaceID != "" && scope.WorkspaceID != other.WorkspaceID {
		scope.Global = true
	}
	for _, key := range other.Keys {
		scope.addKey(key)
	}
	for _, path := range other.Paths {
		scope.addPath(path)
	}
}
