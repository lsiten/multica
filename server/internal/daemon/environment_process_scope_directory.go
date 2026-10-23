package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (scope *environmentMutationScope) addDirectoryBindings(root string) {
	workdir := filepath.Join(root, "workdir")
	if entries, err := os.ReadDir(workdir); err == nil {
		scope.addPath(workdir)
		for _, entry := range entries {
			candidate := filepath.Join(workdir, entry.Name())
			if _, gitErr := os.Lstat(filepath.Join(candidate, ".git")); gitErr == nil {
				scope.addPath(candidate)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		scope.Global = true
	}

	if binding, err := execenv.ReadReviewDirectory(root); err == nil {
		scope.addPath(binding.Path)
		scope.addPath(binding.SourcePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		scope.Global = true
	}
	if retained, err := execenv.ReadRetainedLocalWorktree(root); err == nil {
		scope.addPath(retained.Path)
		scope.addPath(retained.GitRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		scope.Global = true
	}
	if retained, err := execenv.ReadRetainedFamilyWorkspace(root); err == nil {
		scope.addPath(retained.SelectedWorkDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		scope.Global = true
	}
	if provenance, err := execenv.ReadManagedEnvProvenance(root); err == nil {
		scope.addWorkline(*provenance)
	} else if !errors.Is(err, os.ErrNotExist) {
		scope.Global = true
	}
}

func (s *environmentProcessService) operationMutationScope(ctx context.Context, input environmentOperationInput) environmentMutationScope {
	scope := environmentMutationScope{WorkspaceID: input.WorkspaceID}
	if input.Action == "restore" || s.daemon == nil {
		scope.broaden()
		scope.Global = true
		return scope
	}
	paths, err := s.daemon.scopedEnvironmentPaths(ctx, environmentOperationScope{WorkspaceID: input.WorkspaceID, RuntimeID: input.RuntimeID, Automatic: input.Automatic})
	if err != nil {
		scope.broaden()
		scope.Global = true
		return scope
	}
	root, exists := paths[input.Selection.EnvironmentID]
	if !exists {
		scope.broaden()
		scope.Global = true
		return scope
	}
	scope.addPath(root)
	scope.addDirectoryBindings(root)
	return scope
}

func (s *environmentProcessService) reviewMutationScope(ctx context.Context, input protocol.LocalReviewCommand) environmentMutationScope {
	scope := environmentMutationScope{WorkspaceID: input.WorkspaceID}
	if input.Environment != nil || input.Action == "environment" {
		scope.Global = true
		return scope
	}
	scope.addPath(input.Path)
	if s.daemon == nil {
		scope.broaden()
		scope.Global = true
		return scope
	}
	path, root, err := s.daemon.resolveReviewRoot(ctx, worktreeReviewRequest{WorkspaceID: input.WorkspaceID, RuntimeID: input.RuntimeID, TaskID: input.TaskID, Path: input.Path, ProjectResourceID: input.ProjectResourceID})
	if err != nil {
		scope.broaden()
		scope.Global = true
		return scope
	}
	scope.addPath(path)
	scope.addPath(root)
	scope.addDirectoryBindings(root)
	for _, supplied := range input.Paths {
		if filepath.IsAbs(supplied) {
			scope.addPath(supplied)
		}
	}
	return scope
}

// Repository metadata can be shared by sibling worktrees in different workspaces.
func (scope *environmentMutationScope) addGitBoundaries(ctx context.Context) {
	paths := append([]string(nil), scope.Paths...)
	for _, path := range paths {
		if scope.Global {
			return
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			path = filepath.Dir(path)
		}
		for current := path; ; current = filepath.Dir(current) {
			_, err := os.Lstat(filepath.Join(current, ".git"))
			if err == nil {
				scope.addPath(current)
				common, gitErr := worktreeGit(ctx, current, "rev-parse", "--path-format=absolute", "--git-common-dir")
				if gitErr != nil {
					scope.Global = true
					break
				}
				scope.addPath(common)
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				scope.Global = true
				break
			}
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
		}
	}
}
