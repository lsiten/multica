package daemon

import (
	"encoding/json"
	"testing"
	"time"
)

// TestBuildProviderExecOptions is the behaviour-preservation gate for the shared
// launch seam of the F3 provider-run migration: the legacy runner (runTask) and
// the per-execution task worker build an identical agent.ExecOptions from the
// same facts. It asserts the exact field mapping the extraction moved out of
// runTask, so a future change to the shared assembly cannot drift from the
// legacy run.
func TestBuildProviderExecOptions(t *testing.T) {
	// PriorSessionResumeUnavailable makes backendResumeContinuityNotice return
	// "" deterministically, so the test pins the resume mapping without
	// depending on the session-continuity notice wording.
	task := Task{
		ID:                            "task-1",
		PriorSessionID:                "sess-abc",
		PriorSessionResumeUnavailable: true,
	}
	policy := &AutonomyPolicy{MaxTokenCount: 4200, MaxCostUSDTicks: 700}
	in := providerExecOptionsInput{
		Provider:                        "codex",
		Task:                            task,
		AutonomyPolicy:                  policy,
		CostLimitUSDTicks:               700,
		TaskSupplementNegotiated:        true,
		WorkDir:                         "/work/dir",
		ClaudeSettingsPath:              "/work/claude.json",
		QwenpawWorkspace:                "/work/qwenpaw",
		ContextDir:                      "",
		Model:                           "codex-1",
		IdleWatchdogTimeout:             5 * time.Minute,
		ExtraArgs:                       []string{"--extra"},
		CustomArgs:                      []string{"--custom"},
		McpConfig:                       json.RawMessage(`{"mcpServers":{}}`),
		ThinkingLevel:                   "high",
		ServiceTier:                     "priority",
		OpenclawMode:                    "full",
		RuntimeBrief:                    "RUNTIME BRIEF",
		AgentTimeout:                    30 * time.Minute,
		CodexSemanticInactivityTimeout:  10 * time.Minute,
		CodexFirstTurnNoProgressTimeout: 2 * time.Second,
		CodexHandshakeTimeout:           30 * time.Second,
		CodexTurnInterruptTimeout:       2 * time.Second,
		CodexThreadHandshakeTimeout:     60 * time.Second,
	}

	opts := buildProviderExecOptions(in)

	if opts.CostLimitUSDTicks != 700 {
		t.Errorf("CostLimitUSDTicks = %d, want 700", opts.CostLimitUSDTicks)
	}
	if opts.TokenLimit != 4200 {
		t.Errorf("TokenLimit = %d, want 4200", opts.TokenLimit)
	}
	if !opts.EnableTaskSupplement {
		t.Errorf("EnableTaskSupplement = false, want true")
	}
	if opts.Cwd != "/work/dir" {
		t.Errorf("Cwd = %q, want /work/dir", opts.Cwd)
	}
	if opts.Model != "codex-1" {
		t.Errorf("Model = %q, want codex-1", opts.Model)
	}
	if opts.Timeout != 30*time.Minute {
		t.Errorf("Timeout = %s, want 30m", opts.Timeout)
	}
	if opts.SemanticInactivityTimeout != 10*time.Minute {
		t.Errorf("SemanticInactivityTimeout = %s, want 10m", opts.SemanticInactivityTimeout)
	}
	if opts.FirstTurnNoProgressTimeout != 2*time.Second {
		t.Errorf("FirstTurnNoProgressTimeout = %s, want 2s", opts.FirstTurnNoProgressTimeout)
	}
	if opts.IdleWatchdogTimeout != 5*time.Minute {
		t.Errorf("IdleWatchdogTimeout = %s, want 5m", opts.IdleWatchdogTimeout)
	}
	if opts.HandshakeTimeout != 30*time.Second {
		t.Errorf("HandshakeTimeout = %s, want 30s", opts.HandshakeTimeout)
	}
	if opts.TurnInterruptTimeout != 2*time.Second {
		t.Errorf("TurnInterruptTimeout = %s, want 2s", opts.TurnInterruptTimeout)
	}
	if opts.ThreadHandshakeTimeout != 60*time.Second {
		t.Errorf("ThreadHandshakeTimeout = %s, want 60s", opts.ThreadHandshakeTimeout)
	}
	if opts.ResumeSessionID != "sess-abc" {
		t.Errorf("ResumeSessionID = %q, want sess-abc", opts.ResumeSessionID)
	}
	if !opts.ResumeExpected {
		t.Errorf("ResumeExpected = false, want true (PriorSessionID set)")
	}
	if opts.ResumeContinuityNotice != "" {
		t.Errorf("ResumeContinuityNotice = %q, want empty (PriorSessionResumeUnavailable)", opts.ResumeContinuityNotice)
	}
	if got := opts.ExtraArgs; len(got) != 1 || got[0] != "--extra" {
		t.Errorf("ExtraArgs = %v, want [--extra]", got)
	}
	if got := opts.CustomArgs; len(got) != 1 || got[0] != "--custom" {
		t.Errorf("CustomArgs = %v, want [--custom]", got)
	}
	if string(opts.McpConfig) != `{"mcpServers":{}}` {
		t.Errorf("McpConfig = %s", string(opts.McpConfig))
	}
	if opts.ThinkingLevel != "high" {
		t.Errorf("ThinkingLevel = %q, want high", opts.ThinkingLevel)
	}
	if opts.ServiceTier != "priority" {
		t.Errorf("ServiceTier = %q, want priority", opts.ServiceTier)
	}
	if opts.OpenclawMode != "full" {
		t.Errorf("OpenclawMode = %q, want full", opts.OpenclawMode)
	}
	if opts.ClaudeSettingsPath != "/work/claude.json" {
		t.Errorf("ClaudeSettingsPath = %q, want /work/claude.json", opts.ClaudeSettingsPath)
	}
	if opts.QwenpawWorkspace != "/work/qwenpaw" {
		t.Errorf("QwenpawWorkspace = %q, want /work/qwenpaw", opts.QwenpawWorkspace)
	}
	// codex does not need an inline brief, so SystemPrompt must stay empty even
	// with an empty ContextDir.
	if opts.SystemPrompt != "" {
		t.Errorf("SystemPrompt = %q, want empty for codex", opts.SystemPrompt)
	}
}

// TestBuildProviderExecOptionsInlineBrief pins the inline system-prompt gate:
// the full runtime brief is prepended only for providers that cannot load the
// workdir config and only when the provider has no ContextDir.
func TestBuildProviderExecOptionsInlineBrief(t *testing.T) {
	mkInput := func(provider, contextDir string) providerExecOptionsInput {
		return providerExecOptionsInput{
			Provider:     provider,
			ContextDir:   contextDir,
			RuntimeBrief: "BRIEF",
			AgentTimeout: 5 * time.Minute,
		}
	}
	cases := []struct {
		name      string
		in        providerExecOptionsInput
		wantBrief bool
	}{
		{name: "kimi no context dir injects brief", in: mkInput("kimi", ""), wantBrief: true},
		{name: "kimi with context dir does not inject", in: mkInput("kimi", "/ctx"), wantBrief: false},
		{name: "openclaw no context dir injects brief", in: mkInput("openclaw", ""), wantBrief: true},
		{name: "codex never injects", in: mkInput("codex", ""), wantBrief: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := buildProviderExecOptions(c.in)
			if opts.SystemPrompt != "" {
				if !c.wantBrief {
					t.Errorf("SystemPrompt = %q, want empty", opts.SystemPrompt)
				}
				return
			}
			if c.wantBrief {
				t.Errorf("SystemPrompt empty, want brief injected")
			}
		})
	}
}

// TestBuildProviderExecOptionsNilPolicy pins that a nil autonomy policy yields
// zero token/cost limits without panicking, matching the legacy runTask path.
func TestBuildProviderExecOptionsNilPolicy(t *testing.T) {
	opts := buildProviderExecOptions(providerExecOptionsInput{
		Provider: "codex",
		Task:     Task{},
	})
	if opts.TokenLimit != 0 {
		t.Errorf("TokenLimit = %d, want 0 for nil policy", opts.TokenLimit)
	}
	if opts.CostLimitUSDTicks != 0 {
		t.Errorf("CostLimitUSDTicks = %d, want 0", opts.CostLimitUSDTicks)
	}
	if opts.ResumeExpected {
		t.Errorf("ResumeExpected = true, want false (no PriorSessionID)")
	}
}
