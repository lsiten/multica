package daemon

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/internal/daemon/repocache"
	"github.com/multica-ai/multica/server/internal/util"
)

type environmentPhysicalSelection struct {
	PreparationID string                        `json:"preparation_id"`
	Physical      execenv.PhysicalPrepareParams `json:"physical"`
	PriorWorkDir  string                        `json:"prior_work_dir,omitempty"`
	Consumer      execenv.WorktreeConsumer      `json:"consumer"`
	Metadata      *execenv.GCMeta               `json:"metadata,omitempty"`
}

func physicalTaskForSelection(input environmentPhysicalSelection) Task {
	p := input.Physical
	return Task{ID: p.TaskID, WorkspaceID: p.WorkspaceID, WorkspaceSlug: p.WorkspaceSlug, RuntimeID: p.RuntimeID, AgentID: p.AgentID, IssueID: p.IssueID, IssueIdentifier: p.IssueIdentifier, ChatSessionID: p.ChatSessionID, AutopilotID: p.AutopilotID, ProjectID: p.ProjectID, SquadID: p.SquadID, Agent: &AgentData{Name: p.AgentName}, PriorWorkDir: input.PriorWorkDir, DispatchedAt: input.Consumer.DispatchedAt, physicalRepositoryScope: p.RepositoryScope}
}

func (o *environmentPhysicalOwner) claimRoot(reservation execenv.PhysicalRootReservation) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	preparation := o.preparations[reservation.ID]
	if preparation == nil || preparation.Result.Reservation != reservation {
		return errors.New("physical preparation is unknown")
	}
	if err := execenv.VerifyPhysicalRootClaim(reservation, preparation.identity); err != nil {
		return err
	}
	preparation.RootConfirmed = true
	return o.persist(preparation)
}

func (s *environmentProcessService) selectPhysical(ctx context.Context, input environmentPhysicalSelection) (environmentPhysicalResult, error) {
	o := s.physical
	o.mu.Lock()
	preparation := o.preparations[input.PreparationID]
	if preparation == nil || !preparation.RootConfirmed || preparation.Selected || preparation.Result.Reservation.TaskID != input.Physical.TaskID || preparation.Result.Reservation.WorkspaceID != input.Physical.WorkspaceID {
		o.mu.Unlock()
		return environmentPhysicalResult{}, errors.New("physical root selection is not current")
	}
	reservation, identity := preparation.Result.Reservation, preparation.identity
	o.mu.Unlock()
	if err := execenv.VerifyPhysicalRootClaim(reservation, identity); err != nil {
		return environmentPhysicalResult{}, err
	}
	if input.Consumer.TaskID != input.Physical.TaskID || input.Consumer.AgentID != input.Physical.AgentID || input.Consumer.RuntimeID != input.Physical.RuntimeID {
		return environmentPhysicalResult{}, errors.New("physical consumer identity mismatch")
	}
	if !s.daemon.environmentRuntimeOwnedHere(environmentOperationScope{WorkspaceID: input.Physical.WorkspaceID, RuntimeID: input.Physical.RuntimeID}) {
		return environmentPhysicalResult{}, errors.New("physical runtime is not owned")
	}
	task := physicalTaskForSelection(input)
	scope, err := managedScopeForTask(task)
	if err != nil {
		return environmentPhysicalResult{}, err
	}
	input.Physical.WorkspacesRoot = s.daemon.cfg.WorkspacesRoot
	var local *localDirectoryAssignment
	if input.Physical.LocalWorktree != nil || input.Physical.LocalWorkDir != "" {
		path := input.Physical.LocalWorkDir
		mode := ""
		if input.Physical.LocalWorktree != nil {
			path = input.Physical.LocalWorktree.LocalPath
			mode = localDirectoryModeWorktree
		}
		canonical, err := util.ResolveSymlinks(path)
		if err != nil {
			return environmentPhysicalResult{}, err
		}
		local = &localDirectoryAssignment{AbsPath: path, RealPath: canonical, Ref: localDirectoryRef{DaemonID: s.daemon.cfg.DaemonID, LocalPath: path, ExecutionMode: mode}}
	}
	if cache, ok := s.daemon.repoCache.(*repocache.Cache); ok {
		cache.CancelMaintenance()
	}
	if (local == nil || local.UsesWorktree()) && task.AgentID != "" && task.RuntimeID != "" && (task.IssueID != "" || task.ChatSessionID != "" || task.AutopilotID != "") && !input.Physical.PrivateCheckoutRequired {
		release, err := execenv.LockWorklinePreparation(ctx, s.daemon.cfg.WorkspacesRoot, scope)
		if err != nil {
			return environmentPhysicalResult{}, err
		}
		defer release()
	}
	var selected *execenv.Environment
	var prior *executionEnvClaim
	reused := false
	if !input.Physical.PrivateCheckoutRequired || local == nil {
		var priorWorkDir string
		var lockedInfo os.FileInfo
		var usable bool
		prior, priorWorkDir, lockedInfo, usable, err = s.daemon.lockReusablePriorEnvRoot(ctx, task, local, reservation.RootDir)
		if err != nil {
			return environmentPhysicalResult{}, err
		}
		if prior != nil {
			defer prior.Release()
		}
		if usable {
			var worktree *execenv.LocalWorktree
			if local.UsesWorktree() {
				params := localWorktreeParamsForTask(task, local, managedCodeRoot(s.daemon.cfg.WorkspacesRoot, priorWorkDir))
				params.SharedCheckout = prior != nil && prior.shared
				if active, err := localreview.HasActiveReview(ctx, params.EnvRoot, time.Now()); err != nil || active {
					if err != nil {
						return environmentPhysicalResult{}, err
					}
					return environmentPhysicalResult{}, errors.New("local worktree is under active review")
				}
				release, err := s.daemon.acquireReusedWorktreeSnapshot(ctx, task, local, s.daemon.logger)
				if err != nil {
					return environmentPhysicalResult{}, err
				}
				previous, loadErr := execenv.ReadRetainedLocalWorktree(params.EnvRoot)
				if loadErr == nil {
					loadErr = execenv.ReattachRestoredLocalWorktree(ctx, previous, params, s.daemon.logger)
				}
				if loadErr == nil {
					worktree, loadErr = execenv.ReuseLocalWorktree(previous, params, s.daemon.logger)
				}
				release()
				if loadErr != nil && !errors.Is(loadErr, execenv.ErrLocalWorktreeNotReusable) {
					return environmentPhysicalResult{}, loadErr
				}
				usable = loadErr == nil
			}
			if usable {
				selected = execenv.ReusePhysical(execenv.PhysicalReuseParams{WorkspacesRoot: s.daemon.cfg.WorkspacesRoot, RunRoot: reservation.RootDir, WorkDir: priorWorkDir, ReusedLocalWorktree: worktree}, s.daemon.logger)
				if selected != nil && lockedInfo != nil {
					current, err := os.Stat(managedCodeRoot(s.daemon.cfg.WorkspacesRoot, selected.WorkDir))
					if err != nil || !os.SameFile(current, lockedInfo) {
						return environmentPhysicalResult{}, errors.New("physical reuse identity changed")
					}
				}
				reused = selected != nil
			}
		}
	}
	if selected == nil {
		if err := ctx.Err(); err != nil {
			return environmentPhysicalResult{}, context.Cause(ctx)
		}
		selected, err = execenv.PreparePhysicalClaimed(input.Physical, reservation, identity, s.daemon.logger)
		if err != nil {
			return environmentPhysicalResult{}, err
		}
	}
	current, err := os.Stat(reservation.RootDir)
	if err != nil || !os.SameFile(current, identity) {
		return environmentPhysicalResult{}, errors.New("physical run root changed before publication")
	}
	selected.WorkDir, err = util.ResolveSymlinks(selected.WorkDir)
	if err != nil {
		return environmentPhysicalResult{}, err
	}
	if selected.LocalWorktree != nil {
		selected.LocalWorktree.WorkDir = selected.WorkDir
	}
	codeRoot := selected.CodeRootDir
	if codeRoot == "" {
		codeRoot = selected.RootDir
	}
	path := selected.WorkDir
	if selected.LocalWorktree != nil {
		path = selected.LocalWorktree.Path
	}
	bridge, err := execenv.UseSharedDirectory(ctx, path)
	if err != nil {
		return environmentPhysicalResult{}, err
	}
	published := false
	defer func() {
		if !published {
			bridge.Finish(context.Background(), nil)
		}
	}()
	if err = execenv.WriteReviewDirectory(reservation.RootDir, execenv.ReviewDirectory{WorkspaceID: task.WorkspaceID, TaskID: task.ID, Path: selected.WorkDir}); err != nil {
		return environmentPhysicalResult{}, err
	}
	if err = execenv.WriteReviewRuntime(reservation.RootDir, execenv.ReviewRuntime{WorkspaceID: task.WorkspaceID, TaskID: task.ID, RuntimeID: task.RuntimeID, AgentID: task.AgentID, AgentName: input.Physical.AgentName}); err != nil {
		return environmentPhysicalResult{}, err
	}
	if input.Consumer.AgentID != "" && input.Consumer.RuntimeID != "" {
		s.daemon.environmentBindingsMu.Lock()
		err = execenv.UpdateWorktreeConsumer(codeRoot, input.Consumer, false)
		s.daemon.environmentBindingsMu.Unlock()
		if err != nil {
			return environmentPhysicalResult{}, err
		}
	}
	if input.Metadata != nil {
		if input.Metadata.WorkspaceID != task.WorkspaceID || input.Metadata.TaskID != task.ID || input.Metadata.RuntimeID != task.RuntimeID {
			return environmentPhysicalResult{}, errors.New("physical metadata scope mismatch")
		}
		if err = execenv.SaveGCMeta(reservation.RootDir, *input.Metadata); err != nil {
			return environmentPhysicalResult{}, err
		}
	}
	result := physicalResultForEnvironment(selected, reservation)
	result.Reused = reused
	o.mu.Lock()
	preparation.codeIdentity, err = os.Stat(codeRoot)
	if err != nil {
		o.mu.Unlock()
		return environmentPhysicalResult{}, err
	}
	preparation.checkoutIdentity, err = os.Stat(path)
	if err != nil {
		o.mu.Unlock()
		return environmentPhysicalResult{}, err
	}
	preparation.Result = result
	preparation.Physical = selected
	preparation.Selected = true
	preparation.Consumer = input.Consumer
	preparation.bridge = bridge
	err = o.persist(preparation)
	o.mu.Unlock()
	if err != nil {
		return environmentPhysicalResult{}, err
	}
	published = true
	return result, nil
}

func physicalResultForEnvironment(env *execenv.Environment, reservation execenv.PhysicalRootReservation) environmentPhysicalResult {
	result := environmentPhysicalResult{Reservation: reservation, CodeRoot: env.CodeRootDir, WorkDir: env.WorkDir, LocalDirectory: env.LocalDirectory, PrivateCheckout: env.PrivateProviderCheckout}
	if result.CodeRoot == "" {
		result.CodeRoot = env.RootDir
	}
	if wt := env.LocalWorktree; wt != nil {
		result.Branch, result.BaseCommit, result.WorktreePath, result.GitRoot = wt.Branch, wt.BaseCommit, wt.Path, wt.GitRoot
		result.Continued, result.DirtyBaseCaptured = wt.Continued, wt.DirtyBaseCaptured
		result.ReplayConflicts = append([]string(nil), wt.ReplayConflicts...)
		result.RetainCheckout = wt.RetainCheckout
	}
	return result
}

func (s *environmentProcessService) attachPhysical(ctx context.Context, id string, participant execenv.PhysicalParticipant) error {
	o := s.physical
	o.mu.Lock()
	preparation := o.preparations[id]
	if preparation == nil || !preparation.Selected || preparation.Outcome != nil {
		o.mu.Unlock()
		return errors.New("physical selection is not current")
	}
	expectedPath := preparation.Physical.WorkDir
	expectedBranch := ""
	if wt := preparation.Physical.LocalWorktree; wt != nil {
		expectedPath, expectedBranch = wt.Path, wt.Branch
	}
	if participant.Path != expectedPath || participant.Receipt.TaskID != preparation.Result.Reservation.TaskID || participant.Receipt.WorkDir != preparation.Physical.WorkDir || participant.Receipt.Branch != expectedBranch {
		o.mu.Unlock()
		return errors.New("physical attach scope mismatch")
	}
	if preparation.Attached {
		same := preparation.Participant != nil && *preparation.Participant == participant
		o.mu.Unlock()
		if !same {
			return errors.New("physical participant was already bound")
		}
		return execenv.VerifyPhysicalParticipant(ctx, participant)
	}
	o.mu.Unlock()
	if err := verifyPhysicalPreparationIdentity(preparation); err != nil {
		return err
	}
	if err := execenv.VerifyPhysicalParticipant(ctx, participant); err != nil {
		return err
	}
	if err := execenv.ConfirmPhysicalRoot(preparation.Result.Reservation, preparation.identity); err != nil {
		return err
	}
	o.mu.Lock()
	preparation.Attached = true
	preparation.Participant = &participant
	if err := o.persist(preparation); err != nil {
		o.mu.Unlock()
		return err
	}
	// The worker's Git user is now active, so hold the maintenance barrier until
	// the finish outcome is durably recorded. This makes the child's activeTasks
	// reflect physical sessions so the GC loop cannot restart heavy repo
	// maintenance across a live task checkout.
	s.daemon.activeTasks.Add(1)
	bridge := preparation.bridge
	preparation.bridge = nil
	o.mu.Unlock()
	if bridge != nil {
		return bridge.Finish(ctx, nil)
	}
	return nil
}

func (s *environmentProcessService) releasePhysicalConsumer(preparation *environmentPhysicalPreparation) error {
	if preparation.Consumer.TaskID == "" || preparation.Consumer.AgentID == "" || preparation.Consumer.RuntimeID == "" {
		return nil
	}
	current, err := os.Stat(preparation.Result.CodeRoot)
	if err != nil || preparation.codeIdentity == nil || !os.SameFile(current, preparation.codeIdentity) {
		return errors.New("physical code identity changed before consumer release")
	}
	s.daemon.environmentBindingsMu.Lock()
	defer s.daemon.environmentBindingsMu.Unlock()
	return execenv.UpdateWorktreeConsumer(preparation.Result.CodeRoot, preparation.Consumer, true)
}

func (s *environmentProcessService) abortPhysical(ctx context.Context, id string, privateClean bool) error {
	o := s.physical
	o.mu.Lock()
	preparation := o.preparations[id]
	if preparation == nil || preparation.Attached || preparation.Outcome != nil {
		o.mu.Unlock()
		return errors.New("physical abort is not an unstarted preparation")
	}
	bridge := preparation.bridge
	o.mu.Unlock()
	if err := execenv.VerifyPhysicalRootClaim(preparation.Result.Reservation, preparation.identity); err != nil {
		return err
	}
	settle := func(last bool) error {
		if last && !preparation.Result.Reused && privateClean && preparation.Physical.LocalWorktree != nil {
			worktree := preparation.Physical.LocalWorktree
			worktree.Discard(s.daemon.logger)
			if _, err := os.Stat(worktree.Path); !errors.Is(err, os.ErrNotExist) {
				return errors.New("unstarted worktree discard is unconfirmed")
			}
		}
		if preparation.Selected {
			if err := s.releasePhysicalConsumer(preparation); err != nil {
				return err
			}
		}
		return execenv.ConfirmPhysicalRoot(preparation.Result.Reservation, preparation.identity)
	}
	if bridge != nil {
		if err := bridge.Finish(ctx, settle); err != nil {
			return err
		}
	} else if err := settle(true); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	preparation.bridge = nil
	preparation.Outcome = &execenv.LocalWorktreeOutcome{}
	return o.persist(preparation)
}
