package daemon

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// runWorkerProvider owns all provider turns for one admitted execution.
func runWorkerProvider(ctx context.Context, client *ExecutionClient, in *workerContext) (result agent.Result, tools int32, retired string, err error) {
	logger := slog.Default()
	provider := in.Provider
	task := in.Task
	taskCtx := in.Launch.TaskContext
	config := agent.Config{
		ExecutablePath:  in.Launch.ExecutablePath,
		LaunchPrefix:    in.Launch.LaunchPrefix,
		CLIVersion:      in.Launch.CLIVersion,
		Env:             in.Env.Env,
		Logger:          logger,
		TaskID:          task.ID,
		RuntimeID:       task.RuntimeID,
		DaemonVersion:   in.Config.CLIVersion,
		CodexVersion:    in.Launch.CodexVersion,
		BuiltinRuntime:  in.Launch.BuiltinRuntime,
		ProcessLauncher: agent.LocalProcessLauncher{},
	}
	if provider == "hermes" && in.Env.HermesHome != "" {
		if err = agent.StopHermesTaskGateway(config, in.Env.HermesHome); err != nil {
			return
		}
		var stop func()
		stop, err = agent.StartHermesTaskGateway(ctx, config, in.Env.HermesHome)
		if err != nil {
			return
		}
		defer stop()
		defer func() {
			if stopErr := agent.StopHermesTaskGateway(config, in.Env.HermesHome); stopErr != nil {
				logger.Warn("stop task Hermes gateway", "error", stopErr)
			}
		}()
	}
	receipt := in.Launch.Receipt
	writeBrief := func() error {
		var brief string
		var briefErr error
		if in.Env.ContextDir != "" {
			brief, briefErr = execenv.InjectIsolatedRuntimeConfig(in.Env.ContextDir, provider, taskCtx)
		} else {
			brief, briefErr = execenv.InjectRuntimeConfig(in.Env.WorkDir, provider, taskCtx)
		}
		receipt.BriefWritten = briefErr == nil
		if briefErr == nil {
			in.Launch.RuntimeBrief = brief
			receipt.RuntimeBrief = brief
		}
		return briefErr
	}
	prompt := func() string {
		var options []PromptOption
		if in.Env.Delivery != nil {
			wt := in.Env.Delivery
			options = append(options, WithWorktreeReplayConflicts(wt.ReplayConflicts), WithWorktreeBranch(wt.Branch, wt.BaseCommit))
			if wt.CommitsAllLeftovers {
				options = append(options, WithWorktreeCommitsAllLeftovers())
			}
		} else if in.Launch.SharedLocalDirectory {
			options = append(options, WithSharedLocalDirectory())
		}
		text := BuildPrompt(task, provider, options...)
		if in.Env.ContextDir != "" {
			text = in.Launch.RuntimeBrief + "\n\n" + text
		}
		return text
	}
	retired = gateOpenCodeResumeToSaneClock(ctx, &task, &taskCtx, provider, config, in.Env.WorkDir, in.Launch.McpConfig, logger)
	if retired != "" {
		if err = writeBrief(); err != nil {
			return
		}
		in.Task, in.Launch.Prompt = task, prompt()
	}
	var run workerProviderRun
	run, err = buildWorkerProviderRun(*in, provider, logger, workerApproveAll, agent.LocalProcessLauncher{})
	if err != nil {
		return
	}
	var seq atomic.Int32
	phases := newTaskPhaseRecorder(logger, time.Now)
	observer := &taskOutputObserver{}
	ctx = withTaskOutputObserver(ctx, observer)
	reportReceipt := func(fresh bool) {
		receipt.Prompt, receipt.RuntimeBrief = run.Prompt, in.Launch.RuntimeBrief
		receipt.InlineBrief = run.Options.SystemPrompt != "" || in.Env.ContextDir != ""
		receipt.ResumedSession, receipt.FreshRetry = run.Options.ResumeSessionID != "", fresh
		receipt.TaskCtx = taskCtx
		receipt.ExistingInstructions = execenv.ExistingRuntimeConfigContent(in.Env.WorkDir, provider)
		reportCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if reportErr := client.Callback(reportCtx, "context-receipt", map[string]any{"receipt": buildContextReceipt(receipt)}, nil); reportErr != nil {
			logger.Warn("report worker context receipt", "error", reportErr)
		}
	}
	if warning := in.Launch.ThinkingLevelWarning; warning != "" {
		if reportErr := client.ReportTaskMessages(ctx, []TaskMessageData{{Seq: int(seq.Add(1)), Type: "text", Content: warning, CreatedAt: time.Now().UTC()}}); reportErr != nil {
			logger.Warn("report thinking level warning", "error", reportErr)
		}
	}
	var live atomic.Value
	run.Options.OnUsage = func(usage map[string]agent.TokenUsage) {
		if entries := taskUsageEntries(provider, usage); len(entries) > 0 {
			live.Store(entries)
		}
	}
	stopFlush := startTaskUsageFlush(ctx, task.ID, &live, logger, func(c context.Context, _ string, entries []TaskUsageEntry) error {
		return client.Callback(c, "usage", map[string]any{"usage": entries, "snapshot": true}, nil)
	})
	defer stopFlush()
	execute := func(prompt string, opts agent.ExecOptions) (agent.Result, int32, error) {
		return executePreparedProvider(ctx, run.Backend, prompt, opts, logger, task.ID, in.Env.CodexHome, &seq, nil, phases,
			func(c context.Context, _ string, messages []TaskMessageData) error {
				return client.ReportTaskMessages(c, messages)
			},
			func(c context.Context, _, session, dir string) error { return client.PinTaskSession(c, session, dir) },
			workerSupplementSubscribe(), func(c context.Context, _ string) (*TaskSupplement, error) { return client.ClaimSupplement(c) },
			func(c context.Context, _, id string, delivered bool, reason string) error {
				return client.AckSupplement(c, id, delivered, reason)
			},
			defaultTaskSupplementReadyInterval, defaultTaskSupplementPollInterval, in.Config.AgentIdleWatchdog, in.Config.AgentToolWatchdog, in.Config.AgentStartupTimeout)
	}
	reportReceipt(false)
	result, tools, err = execute(run.Prompt, run.Options)
	if err != nil {
		return
	}
	if shouldRetryWithFreshSession(result, task.PriorSessionID, tools, provider) {
		first, firstTools := result, tools
		entries := taskUsageEntries(provider, first.Usage)
		tokens, overflow := reportedUsageTokenCount(entries)
		cost, costExceeded := reportedUsageCostExceeded(entries, run.Options.CostLimitUSDTicks)
		if run.Options.TokenLimit > 0 && (overflow || tokens >= run.Options.TokenLimit) {
			result.TokenBudgetExceeded = true
			return
		}
		if run.Options.CostLimitUSDTicks > 0 && (costExceeded || cost >= run.Options.CostLimitUSDTicks) {
			result.BudgetExceeded = true
			return
		}
		if run.Options.TokenLimit > 0 {
			run.Options.TokenLimit -= tokens
		}
		if run.Options.CostLimitUSDTicks > 0 {
			run.Options.CostLimitUSDTicks -= cost
		}
		if !first.ResumeRejectedTransient {
			retired = task.PriorSessionID
		}
		task.PriorSessionID = ""
		task.PriorSessionResumeUnavailable = true
		taskCtx.PriorSessionResumed = false
		if briefErr := writeBrief(); briefErr != nil {
			logger.Warn("prepare fresh worker runtime brief", "error", briefErr)
		}
		run.Options.ResumeSessionID, run.Options.ResumeContinuityNotice = "", ""
		if providerNeedsInlineSystemPrompt(provider) && in.Env.ContextDir == "" {
			run.Options.SystemPrompt = in.Launch.RuntimeBrief
		}
		run.Prompt = prompt()
		reportReceipt(true)
		retry, retryTools, retryErr := execute(run.Prompt, withPriorTaskUsage(run.Options, first.Usage))
		result, tools = reconcileFreshRetryResult(first, first.Usage, firstTools, retry, retryTools, retryErr)
	}
	if wt := in.Env.Delivery; wt != nil && result.Status == "completed" {
		if problem := wt.CheckDelivery(); problem != nil {
			opts := withPriorTaskUsage(run.Options, result.Usage)
			opts.ResumeSessionID = result.SessionID
			opts.ResumeContinuityNotice = ""
			opts.EnableTaskSupplement = false
			if opts.Timeout <= 0 || opts.Timeout > worktreeRepairTimeout {
				opts.Timeout = worktreeRepairTimeout
			}
			repair, repairTools, repairErr := execute(BuildWorktreeDeliveryRepairPrompt(problem), opts)
			result, tools = mergeWorktreeRepairResult(result, tools, repair, repairTools, repairErr)
		}
	}
	if retired == "" {
		silent, wasSilent := observer.silentFor()
		if shouldRetireSilentResume(result.Status, task.PriorSessionID, silent, wasSilent) {
			retired = task.PriorSessionID
		}
	}
	if result.Status == "cleanup_unconfirmed" {
		err = nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && result.Status != "cleanup_unconfirmed" {
		result.Error = "task autonomy duration limit reached"
	}
	return
}
