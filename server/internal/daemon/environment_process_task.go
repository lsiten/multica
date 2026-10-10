package daemon

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

type environmentTaskHandle struct {
	attached     bool
	privateClean bool
	client       *environmentProcessClient
	result       environmentPhysicalResult
	consumer     execenv.WorktreeConsumer
}

func physicalInputForTask(task Task, agentName string) (execenv.PhysicalPrepareParams, error) {
	scope, err := managedScopeForTask(task)
	if err != nil {
		return execenv.PhysicalPrepareParams{}, err
	}
	key, id := execenv.LocalWorktreeConversation(execenv.PrepareParams{IssueIdentifier: task.IssueIdentifier, Task: execenv.TaskContextForEnv{IssueID: task.IssueID, ChatSessionID: task.ChatSessionID, AutopilotID: task.AutopilotID}})
	return execenv.PhysicalPrepareParams{WorkspaceID: task.WorkspaceID, WorkspaceSlug: task.WorkspaceSlug, TaskID: task.ID, RuntimeID: task.RuntimeID, IssueIdentifier: task.IssueIdentifier, AgentID: task.AgentID, AgentName: agentName, ProjectID: task.ProjectID, SquadID: task.SquadID, IssueID: task.IssueID, ChatSessionID: task.ChatSessionID, AutopilotID: task.AutopilotID, RepositoryScope: scope.RepositoryScope, ConversationKey: key, ConversationID: id}, nil
}

func (d *Daemon) beginEnvironmentTask(ctx context.Context, task Task, agentName string) (*environmentTaskHandle, *execenv.EnvRootClaim, error) {
	child, err := d.ensureEnvironmentProcess(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err = child.sync(ctx); err != nil {
		return nil, nil, err
	}
	input, err := physicalInputForTask(task, agentName)
	if err != nil {
		return nil, nil, err
	}
	var metadata *execenv.GCMeta
	if meta, ok := gcMetaForTask(task); ok {
		metadata = &meta
	}
	var result environmentPhysicalResult
	if err = child.mutation(ctx, "physical.begin_task", struct {
		Physical execenv.PhysicalPrepareParams `json:"physical"`
		Metadata *execenv.GCMeta               `json:"metadata,omitempty"`
	}{input, metadata}, &result); err != nil {
		return nil, nil, err
	}
	if result.Reservation.TaskID != task.ID || result.Reservation.WorkspaceID != task.WorkspaceID {
		return nil, nil, errors.New("physical task root response scope mismatch")
	}
	claim, err := execenv.ClaimPhysicalRoot(d.cfg.WorkspacesRoot, result.Reservation)
	if err != nil {
		return nil, nil, err
	}
	if err = child.mutation(ctx, "physical.root_claim", result.Reservation, nil); err != nil {
		claim.Release()
		return nil, nil, err
	}
	return &environmentTaskHandle{client: child, result: result, consumer: execenv.WorktreeConsumer{TaskID: task.ID, AgentID: task.AgentID, RuntimeID: task.RuntimeID, DispatchedAt: task.DispatchedAt}}, claim, nil
}

func (h *environmentTaskHandle) selectEnvironment(ctx context.Context, input execenv.PhysicalPrepareParams, priorWorkDir string) (*execenv.Environment, error) {
	request := environmentPhysicalSelection{PreparationID: h.result.Reservation.ID, Physical: input, PriorWorkDir: priorWorkDir, Consumer: h.consumer}
	var result environmentPhysicalResult
	if err := h.client.mutation(ctx, "physical.select", request, &result); err != nil {
		return nil, err
	}
	if result.Reservation != h.result.Reservation || result.WorkDir == "" || !filepath.IsAbs(result.WorkDir) || !filepath.IsAbs(result.CodeRoot) {
		return nil, errors.New("physical selection response scope mismatch")
	}
	if !result.LocalDirectory {
		relative, err := filepath.Rel(result.CodeRoot, result.WorkDir)
		if err != nil || !filepath.IsLocal(relative) {
			return nil, errors.New("physical checkout escaped selected code root")
		}
	}
	env := &execenv.Environment{RootDir: result.Reservation.RootDir, WorkDir: result.WorkDir, LocalDirectory: result.LocalDirectory, PrivateProviderCheckout: result.PrivateCheckout, MulticaConfigRoot: filepath.Join(result.Reservation.RootDir, "multica-config")}
	if result.CodeRoot != env.RootDir {
		env.CodeRootDir = result.CodeRoot
	}
	if result.WorktreePath != "" {
		env.LocalWorktree = &execenv.LocalWorktree{GitRoot: result.GitRoot, Path: result.WorktreePath, WorkDir: result.WorkDir, Branch: result.Branch, BaseCommit: result.BaseCommit, RetainCheckout: result.RetainCheckout, Continued: result.Continued, DirtyBaseCaptured: result.DirtyBaseCaptured, ReplayConflicts: append([]string(nil), result.ReplayConflicts...)}
	}
	h.result = result
	return env, nil
}
func (h *environmentTaskHandle) Attach(ctx context.Context, participant execenv.PhysicalParticipant) error {
	err := h.client.mutation(ctx, "physical.attach", struct {
		PreparationID string                      `json:"preparation_id"`
		Participant   execenv.PhysicalParticipant `json:"participant"`
	}{h.result.Reservation.ID, participant}, nil)
	if err == nil {
		h.attached = true
	}
	return err
}

func (h *environmentTaskHandle) BeginFinish(ctx context.Context, participant execenv.PhysicalParticipant, abortReason string) (execenv.PhysicalFinishPermit, error) {
	var result execenv.PhysicalFinishPermit
	err := h.client.mutation(ctx, "physical.finish_begin", struct {
		PreparationID string                      `json:"preparation_id"`
		Participant   execenv.PhysicalParticipant `json:"participant"`
		AbortReason   string                      `json:"abort_reason,omitempty"`
	}{h.result.Reservation.ID, participant, abortReason}, &result)
	return result, err
}
func (h *environmentTaskHandle) ConfirmFinish(ctx context.Context, permit execenv.PhysicalFinishPermit) (execenv.LocalWorktreeOutcome, error) {
	var result execenv.LocalWorktreeOutcome
	err := h.client.mutation(ctx, "physical_finish_confirm", struct {
		PreparationID string                       `json:"preparation_id"`
		Permit        execenv.PhysicalFinishPermit `json:"permit"`
	}{h.result.Reservation.ID, permit}, &result)
	return result, err
}
func (h *environmentTaskHandle) abort(ctx context.Context, privateClean bool) error {
	return h.client.mutation(ctx, "physical.abort", struct {
		PreparationID string `json:"preparation_id"`
		PrivateClean  bool   `json:"private_clean"`
	}{h.result.Reservation.ID, privateClean}, nil)
}

func (d *Daemon) preparePrivateEnvironment(ctx context.Context, params execenv.PrepareParams, physical *execenv.Environment) (*execenv.Environment, error) {
	if d.executionEnvironmentCommand == nil {
		return execenv.PreparePrivate(params, physical, d.logger)
	}
	command, err := d.executionEnvironmentCommand()
	if err != nil {
		return nil, err
	}
	return execenv.PreparePrivateIsolated(ctx, command, params, physical, d.logger)
}
func (d *Daemon) reusePrivateEnvironment(ctx context.Context, params execenv.ReuseParams, physical *execenv.Environment) (*execenv.Environment, error) {
	if d.executionEnvironmentCommand == nil {
		return execenv.ReusePrivate(params, physical, d.logger), nil
	}
	command, err := d.executionEnvironmentCommand()
	if err != nil {
		return nil, err
	}
	return execenv.ReusePrivateIsolated(ctx, command, params, physical, d.logger)
}
