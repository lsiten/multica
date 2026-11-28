package daemon

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func (d *Daemon) executeAndDrain(ctx context.Context, backend agent.Backend, prompt string, opts agent.ExecOptions, taskLog *slog.Logger, taskID, codexHome string, msgSeq *atomic.Int32) (finalResult agent.Result, finalTools int32, finalErr error) {

	defer func() {
		if finalResult.Status == "cleanup_unconfirmed" {
			d.cleanupUnconfirmed.Store(true)
		}
	}()
	taskOutputObserverFromContext(ctx).begin()
	budget := taskBudgetFromContext(ctx)
	source := ""
	if budget != nil {
		source = budget.nextAttempt()
		defer func() {
			budget.RecordSnapshot(source, budget.primaryProvider, finalResult.Usage)
			if errors.Is(budget.Check(), errTaskTokenLimit) {
				finalResult.TokenBudgetExceeded = true
				finalResult.BudgetExceeded = true
			}
			if errors.Is(budget.Check(), errTaskCostLimit) {
				finalResult.BudgetExceeded = true
			}
		}()
	}
	phaseRecorder := taskPhaseRecorderFromContext(ctx)
	// Wrap the caller's ctx so the idle watchdog (below) can interrupt both
	// the agent subprocess (via the ctx passed to backend.Execute) AND the
	// drain loop with a single cancel. Without this layer the backend would
	// stay tied to the parent ctx and our cancellation could only abort
	// drain, leaving the subprocess running.
	agentCtx, agentCancel := context.WithCancel(ctx)
	defer agentCancel()

	session, err := backend.Execute(agentCtx, prompt, opts)
	if err != nil {
		// One provider-agnostic boundary for launches: every backend's
		// cmd.Start() failure arrives here, so diagnosing ENOEXEC at this point
		// covers claude, opencode and any CLI added later without a wrap in
		// each backend (MUL-6164).
		err = agent.ExplainExecError(err)
		taskLog.Debug("backend execute returned error", "error", err)
		return agent.Result{}, 0, err
	}

	if (opts.TokenLimit > 0 || opts.CostLimitUSDTicks > 0) && session.UsageSnapshot == nil {
		if budget != nil {
			_ = budget.RejectUnreportedUsage()
		}
		agentCancel()
		defer func() { finalErr = errTaskUsageUnavailable }()
	}
	defer func() {
		if session.UsageSnapshot != nil {
			if snapshot, known := session.UsageSnapshot(); known {
				finalResult.Usage = reconcileUsageSnapshot(finalResult.Usage, opts.Model, snapshot)
			}
		}
	}()
	// This counter intentionally starts at the narrower provider-session
	// boundary, not at the earlier server-side StartTask transition.
	d.runningTasks.Add(1)
	defer d.runningTasks.Add(-1)
	phaseRecorder.Mark(taskPhaseRuntimeStarted)
	taskLog.Debug("backend started, draining messages")

	// Keep transcript and resume-pointer updates on the daemon client.
	reportTaskMessages := func(reportCtx context.Context, taskID string, messages []TaskMessageData) error {
		return d.client.ReportTaskMessages(reportCtx, taskID, messages)
	}
	pinTaskSession := func(pinCtx context.Context, taskID, sessionID, workDir string) error {
		return d.client.PinTaskSession(pinCtx, taskID, sessionID, workDir)
	}
	claimSupplement := d.client.ClaimTaskSupplement
	ackSupplement := d.client.AckTaskSupplement
	subscribeSupplement := d.taskSupplementSignals.subscribe
	readyInterval := d.effectiveTaskSupplementReadyInterval()
	pollInterval := d.effectiveTaskSupplementPollInterval()
	idleWindow := d.cfg.AgentIdleWatchdog
	toolWindow := d.cfg.AgentToolWatchdog
	startupThreshold := d.cfg.AgentStartupTimeout

	return runProviderExecution(
		ctx, agentCtx, agentCancel, session, opts, budget, source, taskLog, taskID, codexHome, msgSeq, phaseRecorder,
		reportTaskMessages, pinTaskSession, subscribeSupplement, claimSupplement, ackSupplement, readyInterval, pollInterval,
		idleWindow, toolWindow, startupThreshold,
	)
}
