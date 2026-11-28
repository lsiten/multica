package daemon

import (
	"encoding/json"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// providerExecOptionsInput combines the task, environment and provider budgets.
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
	SupplementHookCommand []string
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
	// shares the same task-level limits for initial and retry turns.
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
		SupplementHookCommand:      in.SupplementHookCommand,
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
