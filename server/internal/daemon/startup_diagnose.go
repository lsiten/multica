package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// buildStartupDiagnosis explains why a run that never produced a single
// output message most likely died at launch. The generic idle watchdog waits
// two hours; the startup watchdog fails this dead-on-arrival class in minutes,
// and the text here is what the user sees inline (an "error" transcript row
// plus the task failure comment), so it must be actionable without access to
// the daemon log.
func buildStartupDiagnosis(codexHome string, threshold time.Duration) string {
	isCodex := codexHome != ""
	credentialHint := ""
	if isCodex && !execenv.SharedCodexAuthPresent() {
		credentialHint = " On this machine the Codex credentials file (~/.codex/auth.json) is missing — run `codex login` first; Multica does not sign the Codex CLI in for you."
	}
	processName := "agent"
	if isCodex {
		processName = "Codex"
	}
	return fmt.Sprintf(
		"the %s process emitted no output within %s of starting, so it was stopped as a failed launch. Common causes:%s "+
			"the agent CLI is not signed in (run its login command on the runtime machine); "+
			"the model API is unreachable from the desktop app (GUI-launched apps do not inherit a terminal's proxy/VPN environment); "+
			"or a first-run confirmation prompt is blocking the CLI in a non-interactive session. "+
			"Rebind the agent to a healthy runtime, or inspect the Multica daemon log (execenv/auth lines) for details.",
		processName, threshold, credentialHint,
	)
}

// runStartupWatchdog fails a run that emits ZERO output messages within
// `threshold` of launch. It complements runIdleWatchdog: that one bounds the
// longest legitimate silent step (2h), this one bounds "the process never
// started working" and is deliberately much shorter.
//
// It disarms permanently the moment outputReceived flips (set by the drain
// loop) and never re-arms, so quiet phases after a healthy start — long
// tool runs, big generations — stay entirely on the idle/tool budgets.
//
// On fire it:
//  1. publishes the diagnosis exactly once (inline transcript error row so
//     the chat shows an actionable cause instead of an endless spinner),
//  2. sets fired so executeAndDrain tags the terminal result,
//  3. cancels agentCtx, which kills the agent subprocess exactly like the
//     idle watchdog's cancel.
//
// The buffered-messages guard matches the idle watchdog: a message sitting
// in the channel means the drain loop is behind, not a dead backend, and
// that message is about to flip outputReceived anyway.
func (d *Daemon) runStartupWatchdog(
	agentCtx context.Context,
	threshold time.Duration,
	outputReceived *atomic.Bool,
	fired *atomic.Bool,
	cancel context.CancelFunc,
	messages <-chan agent.Message,
	publishDiagnosis func(string),
	codexHome string,
	taskLog *slog.Logger,
) {
	// Disarm polling derives from the threshold (capped) so small test
	// budgets observe early output promptly; production polls every 45s.
	interval := threshold / 4
	if interval > 45*time.Second {
		interval = 45 * time.Second
	}
	if interval <= 0 {
		interval = threshold
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	deadline := time.NewTimer(threshold)
	defer deadline.Stop()
	for {
		select {
		case <-agentCtx.Done():
			return
		case <-ticker.C:
			// Disarm for good after the first real output.
			if outputReceived.Load() || len(messages) > 0 {
				return
			}
		case <-deadline.C:
			if outputReceived.Load() || len(messages) > 0 {
				return
			}
			if agentCtx.Err() != nil {
				return
			}
			if !fired.CompareAndSwap(false, true) {
				return
			}
			diagnosis := buildStartupDiagnosis(codexHome, threshold)
			taskLog.Warn("startup watchdog firing: agent emitted no output, failing launch",
				"threshold", threshold.String(),
				"codex", codexHome != "",
			)
			publishDiagnosis(diagnosis)
			cancel()
			return
		}
	}
}
