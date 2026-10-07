package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const applicationTicketPruneBatchSize = 1000

func sweepExpiredApplicationTickets(ctx context.Context, queries *db.Queries) {
	pruneCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := queries.PruneApplicationAccessTickets(pruneCtx, applicationTicketPruneBatchSize); err != nil && ctx.Err() == nil {
		slog.Warn("application access ticket cleanup deferred", "error", err)
	}
}

func runApplicationOperationSweeper(ctx context.Context, service *application.Service, bus *events.Bus) {
	runPeriodicSweep(ctx, 30*time.Second, func() {
		roundCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		operations, err := service.ExpireOperations(roundCtx, time.Now(), 16)
		if err != nil && ctx.Err() == nil {
			slog.Warn("application operation expiry deferred", "error", err)
		}
		if bus == nil {
			return
		}
		for _, operation := range operations {
			bus.Publish(events.Event{Type: protocol.EventApplicationChanged, WorkspaceID: operation.WorkspaceID, Payload: map[string]string{"workspace_id": operation.WorkspaceID, "application_id": operation.ApplicationID}})
		}
	})
}
