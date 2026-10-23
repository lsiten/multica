package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestEnvironmentMutationScopeCorruptDirectoryBindingProtectsUnknownResource(t *testing.T) {
	for _, marker := range []string{".review-directory.json", ".local_worktree_reuse.json", ".family-workspace-retained.json", ".managed_env.json"} {
		t.Run(marker, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, marker), []byte(`{`), 0600); err != nil {
				t.Fatal(err)
			}
			scope := environmentMutationScope{WorkspaceID: "ws"}
			scope.addPath(root)
			scope.addDirectoryBindings(root)
			if !scope.Global {
				t.Fatal("unreadable resource binding incorrectly established independence")
			}
		})
	}
}

func TestEnvironmentMutationScopeUnknownCrossWorkspaceResources(t *testing.T) {
	service := &environmentProcessService{}
	other := environmentMutationScope{WorkspaceID: "other"}
	other.addPath(t.TempDir())
	for _, action := range []string{"restore", "clean_cache"} {
		t.Run(action, func(t *testing.T) {
			scope := service.operationMutationScope(t.Context(), environmentOperationInput{WorkspaceID: "ws", Action: action, ArchiveID: "unproven-archive"})
			scopeRequire(t, scope.Global && scope.conflicts(other))
		})
	}
	t.Run("review lookup unresolved", func(t *testing.T) {
		scope := service.reviewMutationScope(t.Context(), protocol.LocalReviewCommand{WorkspaceID: "ws", Path: t.TempDir()})
		scopeRequire(t, scope.Global && scope.conflicts(other))
	})
	for _, kind := range []string{"local_directory", "github_repo", "unknown"} {
		t.Run("source "+kind, func(t *testing.T) {
			raw, err := json.Marshal(environmentApplicationSource{WorkspaceID: "ws", ResourceType: kind, ResourceRef: json.RawMessage(`{}`)})
			scopeRequireNoError(t, err)
			scope, err := service.resolveMutationScope(t.Context(), "application.source", raw)
			scopeRequireNoError(t, err)
			scopeRequire(t, scope.Global && scope.conflicts(other))
		})
	}
}

func TestEnvironmentMutationScopeKnownLocalSourceRemainsIndependent(t *testing.T) {
	path := t.TempDir()
	resource, err := json.Marshal(localDirectoryRef{LocalPath: path})
	scopeRequireNoError(t, err)
	raw, err := json.Marshal(environmentApplicationSource{WorkspaceID: "ws", ResourceType: "local_directory", ResourceRef: resource})
	scopeRequireNoError(t, err)
	service := &environmentProcessService{}
	scope, err := service.resolveMutationScope(t.Context(), "application.source", raw)
	scopeRequireNoError(t, err)
	other := environmentMutationScope{WorkspaceID: "other"}
	other.addPath(t.TempDir())
	scopeRequire(t, !scope.Global && !scope.conflicts(other))
	other.addPath(path)
	scopeRequire(t, scope.conflicts(other))
}

func TestEnvironmentMutationScopeWorkspaceCacheNamespacesStayIndependent(t *testing.T) {
	service := &environmentProcessService{}
	first, err := service.resolveMutationScope(t.Context(), "cache.coauthor", json.RawMessage(`{"workspace_id":"first","enabled":true}`))
	scopeRequireNoError(t, err)
	second, err := service.resolveMutationScope(t.Context(), "cache.coauthor", json.RawMessage(`{"workspace_id":"second","enabled":true}`))
	scopeRequireNoError(t, err)
	scopeRequire(t, !first.Global && !second.Global && !first.conflicts(second))
}
