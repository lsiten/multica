package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/repocache"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (s *environmentProcessService) resolveMutationScope(ctx context.Context, operation string, raw json.RawMessage) (environmentMutationScope, error) {
	scope := environmentMutationScope{}
	switch operation {
	case "physical.recover_handoff":
		var input environmentHandoffRecovery
		if err := decodeEnvironmentPayload(raw, &input); err != nil {
			return scope, err
		}
		return s.preparationMutationScope(input.Reservation.ID), nil
	case "environment.bind":
		var input environmentProcessBinding
		return environmentMutationScope{Global: true}, decodeEnvironmentPayload(raw, &input)
	case "environment.gc":
		return environmentMutationScope{Global: true}, decodeEnvironmentPayload(raw, &struct{}{})
	case "physical.prepare":
		var input execenv.PhysicalPrepareParams
		if err := decodeEnvironmentPayload(raw, &input); err != nil {
			return scope, err
		}
		return s.prepareMutationScope(ctx, input), nil
	case "physical.begin_task":
		var input struct {
			Physical execenv.PhysicalPrepareParams `json:"physical"`
			Metadata *execenv.GCMeta               `json:"metadata,omitempty"`
		}
		if err := decodeEnvironmentPayload(raw, &input); err != nil {
			return scope, err
		}
		return s.prepareMutationScope(ctx, input.Physical), nil
	case "physical.select":
		var input environmentPhysicalSelection
		if err := decodeEnvironmentPayload(raw, &input); err != nil {
			return scope, err
		}
		scope = s.preparationMutationScope(input.PreparationID)
		scope.merge(s.prepareMutationScope(ctx, input.Physical))
		scope.addPath(input.PriorWorkDir)
		return scope, nil
	case "physical.root_claim", "physical.root_confirm":
		var input execenv.PhysicalRootReservation
		if err := decodeEnvironmentPayload(raw, &input); err != nil {
			return scope, err
		}
		return s.preparationMutationScope(input.ID), nil
	case "physical.attach", "physical.abort", "physical.finish_begin", "physical_finish_confirm", "physical.verify_completion":
		var input struct {
			PreparationID string                        `json:"preparation_id"`
			Participant   *execenv.PhysicalParticipant  `json:"participant,omitempty"`
			PrivateClean  bool                          `json:"private_clean,omitempty"`
			AbortReason   string                        `json:"abort_reason,omitempty"`
			Permit        *execenv.PhysicalFinishPermit `json:"permit,omitempty"`
		}
		if err := decodeEnvironmentPayload(raw, &input); err != nil {
			return scope, err
		}
		return s.preparationMutationScope(input.PreparationID), nil
	case "cache.sync":
		var input struct {
			WorkspaceID string               `json:"workspace_id"`
			Repos       []repocache.RepoInfo `json:"repos"`
		}
		if err := decodeEnvironmentPayload(raw, &input); err != nil {
			return scope, err
		}
		scope.WorkspaceID = input.WorkspaceID
		scope.broaden()
		for _, repo := range input.Repos {
			s.addRepositoryMutationScope(&scope, repo.URL)
		}
	case "cache.coauthor":
		var input struct {
			WorkspaceID string `json:"workspace_id"`
			Enabled     bool   `json:"enabled"`
		}
		if err := decodeEnvironmentPayload(raw, &input); err != nil {
			return scope, err
		}
		scope.WorkspaceID = input.WorkspaceID
		scope.broaden()
	case "cache.checkout":
		var input repocache.WorktreeParams
		if err := decodeEnvironmentPayload(raw, &input); err != nil {
			return scope, err
		}
		scope.WorkspaceID = input.WorkspaceID
		s.addRepositoryMutationScope(&scope, input.RepoURL)
		scope.addPath(input.WorkDir)
		if input.WorkDir == "" || input.WorkspaceID == "" {
			scope.broaden()
		}
	case "application.source":
		var input environmentApplicationSource
		if err := decodeEnvironmentPayload(raw, &input); err != nil {
			return scope, err
		}
		scope.WorkspaceID = input.WorkspaceID
		scope.broaden()
		if input.ResourceType == "local_directory" {
			var resource localDirectoryRef
			if err := decodeEnvironmentPayload(input.ResourceRef, &resource); err != nil {
				return scope, err
			}
			scope.addPath(resource.LocalPath)
		} else if input.ResourceType == "github_repo" {
			var resource struct {
				URL string `json:"url"`
				Ref string `json:"ref"`
			}
			if err := decodeEnvironmentPayload(input.ResourceRef, &resource); err != nil {
				return scope, err
			}
			if resource.URL == "" {
				scope.Global = true
			} else {
				s.addRepositoryMutationScope(&scope, resource.URL)
			}
		}
		if len(scope.Paths) == 0 {
			scope.Global = true
		}
	case "review.run":
		var input protocol.LocalReviewCommand
		if err := decodeEnvironmentPayload(raw, &input); err != nil {
			return scope, err
		}
		return s.reviewMutationScope(ctx, input), nil
	case "environment.operation":
		var input environmentOperationInput
		if err := decodeEnvironmentPayload(raw, &input); err != nil {
			return scope, err
		}
		return s.operationMutationScope(ctx, input), nil
	case "worktree-delivery.ack":
		var input execenv.SharedWorktreeDelivery
		if err := decodeEnvironmentPayload(raw, &input); err != nil {
			return scope, err
		}
		scope.addPath(input.WorkDir)
		scope.addPath(input.FilePath)
		scope.addPath(input.Namespace)
		if input.WorkDir == "" || input.FilePath == "" {
			scope.broaden()
		}
	default:
		return scope, errors.New("unsupported environment mutation scope")
	}
	return scope, nil
}

func (s *environmentProcessService) addRepositoryMutationScope(scope *environmentMutationScope, repo string) {
	scope.addKey("repo:" + scope.WorkspaceID + ":" + repo)
	if s.daemon != nil && s.daemon.repoCache != nil {
		scope.addPath(s.daemon.repoCache.Lookup(scope.WorkspaceID, repo))
		scope.addPath(s.daemon.repoCache.BarePath(scope.WorkspaceID, repo))
	}
}
