package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/internal/util"
)

type environmentMutationScope struct {
	Global        bool     `json:"global"`
	WorkspaceID   string   `json:"workspace_id,omitempty"`
	WorkspaceWide bool     `json:"workspace_wide,omitempty"`
	Keys          []string `json:"keys,omitempty"`
	Paths         []string `json:"paths,omitempty"`
}

func (scope environmentMutationScope) conflicts(other environmentMutationScope) bool {
	if scope.Global || other.Global {
		return true
	}
	if scope.WorkspaceWide || other.WorkspaceWide {
		if scope.WorkspaceID == "" || other.WorkspaceID == "" || scope.WorkspaceID == other.WorkspaceID {
			return true
		}
	}
	for _, key := range scope.Keys {
		if slices.Contains(other.Keys, key) {
			return true
		}
	}
	for _, left := range scope.Paths {
		for _, right := range other.Paths {
			if mutationPathContains(left, right) || mutationPathContains(right, left) {
				return true
			}
		}
	}
	// Empty resource descriptions never establish independence.
	return len(scope.Keys)+len(scope.Paths) == 0 && !scope.WorkspaceWide || len(other.Keys)+len(other.Paths) == 0 && !other.WorkspaceWide
}

func mutationPathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && filepath.IsLocal(rel)
}

func (scope *environmentMutationScope) broaden() {
	scope.WorkspaceWide = true
	if scope.WorkspaceID == "" {
		scope.Global = true
	}
}

func (scope *environmentMutationScope) addKey(key string) {
	if len(key) > 4096 {
		scope.Global = true
		return
	}
	if key != "" && !slices.Contains(scope.Keys, key) {
		scope.Keys = append(scope.Keys, key)
	}
	if len(scope.Keys) > 16 {
		scope.Global = true
		scope.Keys = nil
	}
}

func (scope *environmentMutationScope) addPath(path string) {
	if len(path) > 4096 {
		scope.Global = true
		return
	}
	if path == "" {
		return
	}
	if !filepath.IsAbs(path) {
		scope.Global = true
		return
	}
	canonical, err := util.ResolveSymlinks(path)
	if err != nil {
		// An unresolved alias could point into another workspace's local directory.
		scope.Global = true
		return
	}
	if !slices.Contains(scope.Paths, canonical) {
		scope.Paths = append(scope.Paths, canonical)
	}
	if len(scope.Paths) > 16 {
		scope.Global = true
		scope.Paths = nil
	}
}

func (s *environmentProcessService) mutationScopeRead(ctx context.Context, request runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	var input struct {
		Operation string          `json:"operation"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := decodeEnvironmentPayload(request.Payload, &input); err != nil {
		return environmentOperationResult(nil, err)
	}
	if len(input.Payload) == 0 || bytes.Equal(bytes.TrimSpace(input.Payload), []byte("null")) {
		return environmentOperationResult(nil, errors.New("mutation scope payload is required"))
	}
	scope, err := s.resolveMutationScope(ctx, input.Operation, input.Payload)
	if err == nil {
		scope.addGitBoundaries(ctx)
	}
	return environmentOperationResult(scope, err)
}
