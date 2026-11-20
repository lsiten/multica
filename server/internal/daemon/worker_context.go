package daemon

import (
	"encoding/json"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"time"
)

// workerBindRequest is the exact facts control delivers before a provider launch.
// The grant is the scoped mwt_ credential; the remaining flags are control's
// observation of the bind/Start/launch acknowledgement. The worker applies the
// closed launch-authorization contract to them and never launches on its own.
type workerBindRequest struct {
	Grant               protocol.ExecutionGrantResponse `json:"grant"`
	CallbackURL         string                          `json:"callback_url"`
	WorkerIDMatchesBind bool                            `json:"worker_id_matches_bind"`
	LaunchAuthorized    bool                            `json:"launch_authorized"`
	Uncertain           bool                            `json:"uncertain"`
	// Context is the execution context the control parent delivers so the worker
	// can build and launch the provider run without a *Daemon. Absent until the
	// control parent supports context delivery; the worker stays launchable for
	// the authorization gate but cannot run a provider without it (fail-closed).
	Context *workerContext `json:"context,omitempty"`
}

// workerContext is the exact facts the control parent delivers so a worker can
// build the provider run (ExecOptions, backend, prompt) and drive
// runProviderExecution. It carries no *Daemon reference and no secret: the
// scoped mwt_ grant is delivered separately as the worker's own credential.
type workerContext struct {
	supplements *taskSupplementSignals
	// Provider is the runtime identity (e.g. "codex", "opencode", "kimi") the
	// control parent delivers so the worker resolves the exact provider it is
	// authorized to run.
	Provider string
	// Config is the daemon-wide configuration the worker reuses (timeouts,
	// budgets, and agent entries).
	Config Config
	// Task is the server-claimed task; it supplies the resume session, thread
	// name, resume continuity, and the prompt brief.
	Task Task
	// Env is the prepared execution environment (workdir, codex home, mcp, and
	// the resolved agent environment) so the backend resolves identically.
	Env PreparedEnv
	// Launch carries the resolved provider launch facts assembled by control.
	// Keeping these facts in the bind payload prevents the worker from silently
	// dropping runtime profiles, MCP overlays, model selection, or task briefs.
	Launch workerLaunchFacts
}

type workerLaunchFacts struct {
	SharedLocalDirectory     bool                      `json:"shared_local_directory"`
	Prompt                   string                    `json:"prompt"`
	TaskContext              execenv.TaskContextForEnv `json:"task_context"`
	Receipt                  contextReceiptInput       `json:"receipt"`
	CursorHookCommand        []string                  `json:"cursor_hook_command"`
	ThinkingLevelWarning     string                    `json:"thinking_level_warning"`
	OpenclawModelOverride    string                    `json:"openclaw_model_override"`
	ExecutablePath           string                    `json:"executable_path,omitempty"`
	LaunchPrefix             []string                  `json:"launch_prefix,omitempty"`
	CLIVersion               string                    `json:"cli_version,omitempty"`
	CodexVersion             string                    `json:"codex_version,omitempty"`
	BuiltinRuntime           bool                      `json:"builtin_runtime"`
	Model                    string                    `json:"model,omitempty"`
	IdleWatchdogTimeout      time.Duration             `json:"idle_watchdog_timeout,omitempty"`
	ExtraArgs                []string                  `json:"extra_args,omitempty"`
	CustomArgs               []string                  `json:"custom_args,omitempty"`
	McpConfig                json.RawMessage           `json:"mcp_config,omitempty"`
	ThinkingLevel            string                    `json:"thinking_level,omitempty"`
	ServiceTier              string                    `json:"service_tier,omitempty"`
	OpenclawMode             string                    `json:"openclaw_mode,omitempty"`
	RuntimeBrief             string                    `json:"runtime_brief,omitempty"`
	TaskSupplementNegotiated bool                      `json:"task_supplement_negotiated"`
}

// PreparedEnv is the prepared execution environment delivered to a worker. It is
// the JSON-safe projection of the environment control prepares in runTask: the
// workdir, codex home, and resolved agent environment map.
type PreparedEnv struct {
	PiSkillPaths       []string                    `json:"pi_skill_paths"`
	HermesHome         string                      `json:"hermes_home"`
	Delivery           *execenv.WorktreeInspection `json:"delivery,omitempty"`
	WorkDir            string                      `json:"work_dir"`
	RootDir            string                      `json:"root_dir,omitempty"`
	LocalWorktree      bool                        `json:"local_worktree,omitempty"`
	CodexHome          string                      `json:"codex_home"`
	ClaudeSettingsPath string                      `json:"claude_settings_path,omitempty"`
	QwenpawWorkspace   string                      `json:"qwenpaw_workspace,omitempty"`
	ContextDir         string                      `json:"context_dir,omitempty"`
	CursorMcpPluginDir string                      `json:"cursor_mcp_plugin_dir,omitempty"`
	OmpMcpExtensionDir string                      `json:"omp_mcp_extension_dir,omitempty"`
	Env                map[string]string           `json:"env,omitempty"`
}
