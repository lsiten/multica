package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// providerExecOptionsInput is the exact set of facts needed to assemble the
// provider ExecOptions for one execution. The task worker builds it from the
// claimed task, prepared environment, resolved model and arguments, and
// provider budgets.
//
// Keeping the assembly free of *Daemon state makes the child launch contract
// explicit and testable.
type providerExecOptionsInput struct {
	// Provider is the runtime identity (e.g. "codex", "opencode", "kimi").
	Provider string
	// Task is the server-claimed task; it supplies the resume session, the
	// thread name, and the resume continuity notice.
	Task Task
	// AutonomyPolicy is the parsed task autonomy policy (nil when absent); it
	// supplies the token limit. CostLimitUSDTicks is derived from the same
	// policy by the caller (effectiveAutonomyCostLimit) so the two limits agree.
	AutonomyPolicy    *AutonomyPolicy
	CostLimitUSDTicks int64
	// TaskSupplementNegotiated records whether the server capability handshake
	// enabled task supplements for this execution.
	TaskSupplementNegotiated bool

	// WorkDir, ClaudeSettingsPath, and QwenpawWorkspace come from the prepared
	// execution environment. ContextDir gates the inline system-prompt
	// injection: empty means the provider loads the runtime config from the
	// workdir, so the brief is not also injected inline.
	WorkDir            string
	ClaudeSettingsPath string
	QwenpawWorkspace   string
	ContextDir         string
	CursorMcpPluginDir string
	OmpMcpExtensionDir string

	// Model is the resolved provider model ("") lets the CLI pick its default.
	Model                 string
	CursorHookCommand     []string
	PiSkillPaths          []string
	OpenclawModelOverride string
	TaskToken             string
	MulticaServerURL      string
	MulticaWorkspaceID    string
	// IdleWatchdogTimeout narrows the no-message watchdog for opencode/codearts;
	// zero keeps the daemon-wide window.
	IdleWatchdogTimeout time.Duration

	// ExtraArgs, CustomArgs, McpConfig, ThinkingLevel, ServiceTier, and
	// OpenclawMode are the per-task/daemon argument and overlay assembly.
	ExtraArgs     []string
	CustomArgs    []string
	McpConfig     json.RawMessage
	ThinkingLevel string
	ServiceTier   string
	OpenclawMode  string
	// RuntimeBrief is the full runtime brief prepended inline for providers that
	// cannot reliably load the workdir config (see providerNeedsInlineSystemPrompt).
	RuntimeBrief string

	// The daemon-wide provider budgets are passed explicitly so the shared seam
	// has no *Daemon reference and the worker can supply its own.
	AgentTimeout                    time.Duration
	CodexSemanticInactivityTimeout  time.Duration
	CodexFirstTurnNoProgressTimeout time.Duration
	CodexHandshakeTimeout           time.Duration
	CodexTurnInterruptTimeout       time.Duration
	CodexThreadHandshakeTimeout     time.Duration
}

// buildProviderExecOptions assembles the provider ExecOptions for one execution
// from its input facts. It reads no daemon state and mutates nothing.
func buildProviderExecOptions(in providerExecOptionsInput) agent.ExecOptions {
	execOpts := agent.ExecOptions{
		CostLimitUSDTicks:          in.CostLimitUSDTicks,
		TokenLimit:                 autonomyPolicyTokenLimit(in.AutonomyPolicy),
		EnableTaskSupplement:       in.TaskSupplementNegotiated,
		CursorHookCommand:          in.CursorHookCommand,
		PiSkillPaths:               in.PiSkillPaths,
		OpenclawModelOverride:      in.OpenclawModelOverride,
		TaskToken:                  in.TaskToken,
		MulticaServerURL:           in.MulticaServerURL,
		MulticaWorkspaceID:         in.MulticaWorkspaceID,
		Cwd:                        in.WorkDir,
		Model:                      in.Model,
		ThreadName:                 deriveTaskThreadName(in.Task),
		Timeout:                    in.AgentTimeout,
		SemanticInactivityTimeout:  in.CodexSemanticInactivityTimeout,
		FirstTurnNoProgressTimeout: in.CodexFirstTurnNoProgressTimeout,
		IdleWatchdogTimeout:        in.IdleWatchdogTimeout,
		HandshakeTimeout:           in.CodexHandshakeTimeout,
		TurnInterruptTimeout:       in.CodexTurnInterruptTimeout,
		ThreadHandshakeTimeout:     in.CodexThreadHandshakeTimeout,
		ResumeSessionID:            in.Task.PriorSessionID,
		// PriorSessionID here already reflects the pre-flight resume gates (a
		// dropped resume is surfaced via the prompt instead). If it survived to
		// here, the backend must disclose the loss when the live resume still
		// fails — even across the fresh-session retry, which clears
		// ResumeSessionID but not this (MUL-4424). The disclosure wording is
		// owned by the caller because only it knows whether the surface's
		// conversation is still readable; empty when the prompt already carries
		// the notice, so a turn can never pay for it twice (MUL-5722).
		ResumeExpected:         in.Task.PriorSessionID != "",
		ResumeContinuityNotice: backendResumeContinuityNotice(in.Task),
		ExtraArgs:              in.ExtraArgs,
		CustomArgs:             in.CustomArgs,
		McpConfig:              in.McpConfig,
		ThinkingLevel:          in.ThinkingLevel,
		ServiceTier:            in.ServiceTier,
		OpenclawMode:           in.OpenclawMode,
		ClaudeSettingsPath:     in.ClaudeSettingsPath,
		CursorMcpPluginDir:     in.CursorMcpPluginDir,
		OmpMcpExtensionDir:     in.OmpMcpExtensionDir,
		QwenpawWorkspace:       in.QwenpawWorkspace,
	}
	// Some providers do not reliably load the per-task runtime config files we
	// write into the task workdir: openclaw is pinned to the workdir via a
	// synthesized config, but older releases and kimi's opaque cwd handling can
	// miss it. Prepend the full runtime brief inline (CLI catalog + workflow
	// steps + agent identity/persona + skills + project context) so the backend
	// picks up the same payload file-based runtimes read from disk. Without
	// this those providers silently miss the workflow section and never call
	// `multica issue status` / `multica issue comment add`, leaving issues stuck
	// in `todo`. Hermes and Kiro are excluded: their ACP sessions start in the
	// task cwd and load AGENTS.md themselves, so an inline brief would duplicate
	// that context and bloat every turn.
	if providerNeedsInlineSystemPrompt(in.Provider) && in.ContextDir == "" {
		execOpts.SystemPrompt = in.RuntimeBrief
	}
	return execOpts
}

// providerBackendInput is the exact set of facts needed to resolve the agent
// backend for one execution. It is the backend-resolution input used by the task worker, including the
// bound task, resolved executable, assembled environment, and child-local seams.
//
// The approval callback and logger are passed in rather than read from *Daemon
// so the seam has no daemon reference: the worker supplies its own. This is a
// pure, side-effect-free assembly — its behaviour is covered by the existing
// runTask suite and resolveProviderBackend is exercised via that same call.
type providerBackendInput struct {
	Provider string
	Task     Task
	// Logger is the daemon-wide logger (runTask passes d.logger; the worker
	// passes its own). New fills the safe launch log with it.
	Logger *slog.Logger
	// RequestApproval is the task-scoped approval callback (runTask passes
	// d.requestTaskApproval(task); the worker passes its own). nil or an error
	// return fails closed onto the provider's standard behaviour.
	RequestApproval func(context.Context, agent.ApprovalRequest) (bool, error)
	// ExecutablePath is the resolved CLI binary path (entry.Path).
	ExecutablePath string
	// LaunchPrefix is the custom runtime profile's fixed_args (profileFixedArgs).
	LaunchPrefix []string
	// CLIVersion is the detected version paired with ExecutablePath.
	CLIVersion string
	// Env is the assembled agent environment (agentEnv).
	Env map[string]string
	// DaemonVersion is the running daemon's own version (d.cfg.CLIVersion).
	DaemonVersion string
	// CodexVersion is the detected Codex CLI version.
	CodexVersion string
	// BuiltinRuntime reports that ExecutablePath is the provider's own discovered
	// binary rather than a custom runtime profile's command (!usesCustomProfileCommand).
	BuiltinRuntime bool
	// ProcessLauncher controls where backend processes are created (LocalProcessLauncher).
	ProcessLauncher agent.ProcessLauncher
}

// resolveProviderBackend resolves the agent backend for one execution through
// the unified runtime resolver. It reads no daemon state and mutates nothing.
func resolveProviderBackend(in providerBackendInput) (agent.Backend, error) {
	return agent.ResolveBackend(in.Provider, agent.Config{
		RequestApproval: in.RequestApproval,
		ExecutablePath:  in.ExecutablePath,
		LaunchPrefix:    in.LaunchPrefix,
		CLIVersion:      in.CLIVersion,
		Env:             in.Env,
		Logger:          in.Logger,
		TaskID:          in.Task.ID,
		RuntimeID:       in.Task.RuntimeID,
		DaemonVersion:   in.DaemonVersion,
		CodexVersion:    in.CodexVersion,
		BuiltinRuntime:  in.BuiltinRuntime,
		ProcessLauncher: in.ProcessLauncher,
	})
}

// workerProviderRun is the fully assembled provider run a worker launches from its
// delivered context: the ExecOptions, the resolved backend, and the prompt. It is
// the *Daemon-free assembly the per-execution task worker uses to genuinely run
// the provider (the control parent only assembles the child-worker request). It reads only the delivered context and the passed seams; it holds no
// *Daemon reference.
type workerProviderRun struct {
	Backend agent.Backend
	Options agent.ExecOptions
	Prompt  string
}

// buildWorkerProviderRun assembles the provider run from the context the control
// parent delivered. It is the Daemon-free launch seam the task worker calls:
// it reuses buildProviderExecOptions and resolveProviderBackend, then returns
// the ready-to-run provider. provider is the runtime identity the worker runs (the control
// parent delivers it, mirroring runTask's provider param). A missing provider,
// agent, or an unresolved backend yields an error so the worker fails closed
// instead of fabricating a run.
func buildWorkerProviderRun(in workerContext, provider string, logger *slog.Logger, requestApproval func(context.Context, agent.ApprovalRequest) (bool, error), processLauncher agent.ProcessLauncher) (workerProviderRun, error) {
	if provider == "" {
		return workerProviderRun{}, errors.New("worker context has no provider")
	}
	if in.Task.Agent == nil {
		return workerProviderRun{}, errors.New("worker context task has no agent")
	}
	autonomyPolicy, err := parseAutonomyPolicy(in.Task.Agent.RuntimeConfig)
	if err != nil {
		return workerProviderRun{}, err
	}
	costLimitUSDTicks := int64(0)
	if autonomyPolicy != nil {
		costLimitUSDTicks = effectiveAutonomyCostLimit(autonomyPolicy)
	}
	facts := in.Launch
	model := facts.Model
	if model == "" {
		model = resolveWorkerModel(provider, in.Task, in.Config)
	}
	extraArgs := facts.ExtraArgs
	if extraArgs == nil {
		extraArgs = defaultArgsForProvider(in.Config, provider)
	}
	customArgs := facts.CustomArgs
	if customArgs == nil && in.Task.Agent != nil {
		customArgs = in.Task.Agent.CustomArgs
	}
	mcpConfig := facts.McpConfig
	if len(mcpConfig) == 0 && in.Task.Agent != nil {
		mcpConfig = in.Task.Agent.McpConfig
	}
	thinkingLevel := facts.ThinkingLevel
	if thinkingLevel == "" && in.Task.Agent != nil {
		thinkingLevel = in.Task.Agent.ThinkingLevel
	}
	serviceTier := facts.ServiceTier
	if serviceTier == "" && in.Task.Agent != nil {
		serviceTier = in.Task.Agent.ServiceTier
	}
	cliVersion := facts.CLIVersion
	if cliVersion == "" {
		cliVersion = in.Config.CLIVersion
	}
	execOpts := buildProviderExecOptions(providerExecOptionsInput{
		Provider:                        provider,
		Task:                            in.Task,
		AutonomyPolicy:                  autonomyPolicy,
		CostLimitUSDTicks:               costLimitUSDTicks,
		TaskSupplementNegotiated:        facts.TaskSupplementNegotiated,
		WorkDir:                         in.Env.WorkDir,
		CursorHookCommand:               facts.CursorHookCommand,
		PiSkillPaths:                    in.Env.PiSkillPaths,
		OpenclawModelOverride:           facts.OpenclawModelOverride,
		TaskToken:                       in.Env.Env["MULTICA_TOKEN"],
		MulticaServerURL:                in.Config.ServerBaseURL,
		MulticaWorkspaceID:              in.Task.WorkspaceID,
		ClaudeSettingsPath:              in.Env.ClaudeSettingsPath,
		QwenpawWorkspace:                in.Env.QwenpawWorkspace,
		ContextDir:                      in.Env.ContextDir,
		CursorMcpPluginDir:              in.Env.CursorMcpPluginDir,
		OmpMcpExtensionDir:              in.Env.OmpMcpExtensionDir,
		Model:                           model,
		IdleWatchdogTimeout:             facts.IdleWatchdogTimeout,
		ExtraArgs:                       extraArgs,
		CustomArgs:                      customArgs,
		McpConfig:                       mcpConfig,
		ThinkingLevel:                   thinkingLevel,
		ServiceTier:                     serviceTier,
		OpenclawMode:                    facts.OpenclawMode,
		RuntimeBrief:                    facts.RuntimeBrief,
		AgentTimeout:                    in.Config.AgentTimeout,
		CodexSemanticInactivityTimeout:  in.Config.CodexSemanticInactivityTimeout,
		CodexFirstTurnNoProgressTimeout: in.Config.CodexFirstTurnNoProgressTimeout,
		CodexHandshakeTimeout:           in.Config.CodexHandshakeTimeout,
		CodexTurnInterruptTimeout:       in.Config.CodexTurnInterruptTimeout,
		CodexThreadHandshakeTimeout:     in.Config.CodexThreadHandshakeTimeout,
	})
	backend, err := resolveProviderBackend(providerBackendInput{
		Provider:        provider,
		Task:            in.Task,
		Logger:          logger,
		RequestApproval: requestApproval,
		ExecutablePath:  facts.ExecutablePath,
		LaunchPrefix:    facts.LaunchPrefix,
		CLIVersion:      cliVersion,
		Env:             in.Env.Env,
		DaemonVersion:   in.Config.CLIVersion,
		CodexVersion:    facts.CodexVersion,
		BuiltinRuntime:  facts.BuiltinRuntime,
		ProcessLauncher: processLauncher,
	})
	if err != nil {
		return workerProviderRun{}, err
	}
	prompt := facts.Prompt
	if prompt == "" {
		prompt = BuildPrompt(in.Task, provider)
	}
	return workerProviderRun{Backend: backend, Options: execOpts, Prompt: prompt}, nil
}

// resolveWorkerModel picks the model the worker launches with: an explicit
// agent.model wins, then the daemon config's agent entry for the provider.
func resolveWorkerModel(provider string, task Task, cfg Config) string {
	if task.Agent != nil && task.Agent.Model != "" {
		return task.Agent.Model
	}
	if entry, ok := cfg.Agents[provider]; ok {
		return entry.Model
	}
	return ""
}

// executePreparedProvider genuinely runs one already-assembled provider run: it
// creates the agent context, launches the backend, and drives the shared,
// *Daemon-free runProviderExecution. It is the *Daemon-free driver the per-
// execution task worker (F3) uses to run the provider without a *Daemon: the
// The caller owns budget snapshots, usage reconciliation and health accounting.
//
// The transcript report, session pin, supplement claim/ack, supplement signal
// subscription, and watchdog windows are supplied by the worker scoped client.
// A nil budget is tolerated by runProviderExecution; a nil phase recorder is not.
func executePreparedProvider(
	ctx context.Context,
	backend agent.Backend,
	prompt string,
	opts agent.ExecOptions,
	taskLog *slog.Logger,
	taskID, codexHome string,
	msgSeq *atomic.Int32,
	budget *taskUsageBudget,
	phaseRecorder *taskPhaseRecorder,
	reportTaskMessages func(context.Context, string, []TaskMessageData) error,
	pinTaskSession func(context.Context, string, string, string) error,
	subscribeSupplement func(string) (<-chan struct{}, func()),
	claimSupplement func(context.Context, string) (*TaskSupplement, error),
	ackSupplement func(context.Context, string, string, bool, string) error,
	readyInterval, pollInterval, idleWindow, toolWindow, startupThreshold time.Duration,
) (agent.Result, int32, error) {
	agentCtx, agentCancel := context.WithCancel(ctx)
	defer agentCancel()
	session, err := backend.Execute(agentCtx, prompt, opts)
	if err != nil {
		// Mirror the provider launch-error boundary: an unconfirmed
		// vscreen stop is reported as such, every other backend start failure
		// is explained once here so the worker does not fabricate a result.
		if errors.Is(context.Cause(ctx), errVscreenIntervention) {
			return agent.Result{}, 0, errors.Join(errVscreenStopUnconfirmed, err)
		}
		taskLog.Debug("worker backend execute returned error", "error", err)
		return agent.Result{}, 0, agent.ExplainExecError(err)
	}
	taskOutputObserverFromContext(ctx).begin()
	phaseRecorder.Mark(taskPhaseRuntimeStarted)
	taskLog.Debug("worker backend started, draining messages")
	return runProviderExecution(
		ctx, agentCtx, agentCancel, session, opts, budget, "", taskLog, taskID, codexHome, msgSeq, phaseRecorder,
		reportTaskMessages, pinTaskSession, subscribeSupplement, claimSupplement, ackSupplement, readyInterval, pollInterval,
		idleWindow, toolWindow, startupThreshold,
	)
}
