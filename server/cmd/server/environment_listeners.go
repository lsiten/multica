package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func registerEnvironmentListeners(bus *events.Bus, queries *db.Queries, notifier handler.DaemonPendingWorkNotifier) {
	for _, eventType := range []string{protocol.EventIssueUpdated, protocol.EventIssueDeleted, protocol.EventTaskCompleted, protocol.EventTaskFailed, protocol.EventTaskCancelled, protocol.EventHumanRequestChanged, protocol.EventChatSessionUpdated, protocol.EventChatSessionDeleted, protocol.EventAutopilotRunDone} {
		bus.Subscribe(eventType, func(event events.Event) {
			workspace, err := util.ParseUUID(event.WorkspaceID)
			if err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			runtimes, err := queries.ListAgentRuntimes(ctx, workspace)
			if err != nil {
				slog.Warn("environment lifecycle wakeup unavailable", "workspace_id", event.WorkspaceID, "error", err)
				return
			}
			for _, runtime := range runtimes {
				if runtime.RuntimeMode == "local" {
					notifier.NotifyPendingWork(util.UUIDToString(runtime.ID), protocol.PendingWorkKindEnvironment)
				}
			}
		})
	}
}
