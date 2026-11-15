package daemon

import (
	"context"

	"github.com/multica-ai/multica/server/internal/agentdesktop"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) handleAgentDesktopReadinessQuery(ctx context.Context, query protocol.AgentDesktopReadinessQuery, enqueue func([]byte) (*wsOutbound, error), generation mirrorConnectionGeneration) {
	if query.Validate() != nil || !d.mirrorEnvelopeCurrent(query.RuntimeEnvelope, generation) {
		return
	}
	readiness, _ := agentdesktop.Inspect(ctx)
	result := protocol.AgentDesktopReadinessResult{
		RuntimeEnvelope: query.RuntimeEnvelope,
		Readiness:       readiness,
	}
	if err := d.sendVscreen(enqueue, generation, protocol.EventAgentDesktopReadinessResult, result); err != nil {
		d.logger.Debug("agent-desktop readiness result dropped", "error", err)
	}
}

func (d *Daemon) handleAgentDesktopPermissionGuideQuery(ctx context.Context, query protocol.AgentDesktopPermissionGuideQuery, enqueue func([]byte) (*wsOutbound, error), generation mirrorConnectionGeneration) {
	if query.Validate() != nil || !d.mirrorEnvelopeCurrent(query.RuntimeEnvelope, generation) {
		return
	}
	var readiness protocol.AgentDesktopReadiness
	if err := agentdesktop.OpenPermissionGuide(ctx); err != nil {
		readiness = failedAgentDesktopReadiness(protocol.AgentDesktopReasonGuideFailed)
	} else {
		readiness, _ = agentdesktop.Inspect(ctx)
	}
	result := protocol.AgentDesktopPermissionGuideResult{
		RuntimeEnvelope: query.RuntimeEnvelope,
		Readiness:       readiness,
	}
	if err := d.sendVscreen(enqueue, generation, protocol.EventAgentDesktopPermissionGuideResult, result); err != nil {
		d.logger.Debug("agent-desktop permission guide result dropped", "error", err)
	}
}

func failedAgentDesktopReadiness(reason string) protocol.AgentDesktopReadiness {
	return protocol.AgentDesktopReadiness{
		Permissions: protocol.AgentDesktopPermissions{
			Accessibility:   protocol.AgentDesktopPermissionUnknown,
			ScreenRecording: protocol.AgentDesktopPermissionUnknown,
			Automation:      protocol.AgentDesktopPermissionUnknown,
		},
		Reason: reason,
	}
}
