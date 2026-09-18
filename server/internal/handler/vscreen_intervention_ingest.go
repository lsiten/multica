package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// DaemonVscreenIntervention shares the HTTP report's native verification and transaction.
func (h *Handler) DaemonVscreenIntervention(ctx context.Context, connection daemonws.VscreenConnection, report protocol.VscreenIntervention) (int64, string) {
	row, err := h.ingestVscreenIntervention(ctx, connection, report)
	if err != nil {
		_, reason := interventionReportError(err)
		return 0, reason
	}
	return row.Version, ""
}

func (h *Handler) ingestVscreenIntervention(ctx context.Context, connection daemonws.VscreenConnection, report protocol.VscreenIntervention) (db.RuntimeVscreenIntervention, error) {
	var row db.RuntimeVscreenIntervention
	err := connection.WithVscreenLifecycle(ctx, func() error {
		var err error
		row, err = h.ingestCurrentVscreenIntervention(ctx, connection, report)
		return err
	})
	return row, err
}
func (h *Handler) ingestCurrentVscreenIntervention(ctx context.Context, connection daemonws.VscreenConnection, report protocol.VscreenIntervention) (db.RuntimeVscreenIntervention, error) {
	var row db.RuntimeVscreenIntervention
	runtimeID, err := util.ParseUUID(report.RuntimeID)
	if err != nil || report.Validate() != nil {
		return row, service.InterventionError("invalid_report")
	}
	identity := connection.Identity()
	rt, err := h.Queries.GetAgentRuntime(ctx, runtimeID)
	if err != nil {
		return row, err
	}
	if uuidToString(rt.WorkspaceID) != report.WorkspaceID || !rt.DaemonID.Valid || rt.DaemonID.String != identity.DaemonID {
		return row, service.InterventionError("permission_denied")
	}
	if identity.UserID != "" {
		if uuidToString(rt.OwnerID) != identity.UserID {
			return row, service.InterventionError("permission_denied")
		}
		_, err = h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: rt.OwnerID, WorkspaceID: rt.WorkspaceID})
		if err != nil {
			return row, service.InterventionError("permission_denied")
		}
	}
	current, err := h.DaemonHub.QueryVscreen(ctx, report.WorkspaceID, report.RuntimeID, identity.DaemonID, "state")
	if err != nil {
		return row, err
	}
	state := current.State
	if current.DaemonGeneration != report.DaemonGeneration || state == nil || current.Reason != "" || state.InterventionID == nil || *state.InterventionID != report.InterventionID || state.InterventionState != report.State || state.ReturnReceiptID != report.ReturnReceiptID || state.NativeEpoch != report.Epoch.NativeEpoch || state.DisplayGeneration != report.Epoch.DisplayGeneration || state.GeometryRevision != report.Epoch.GeometryRevision {
		return row, service.InterventionError("stale_generation")
	}
	err = connection.WithCurrent(ctx, func() error {
		var persistErr error
		row, persistErr = h.TaskService.ReportVscreenIntervention(ctx, report)
		return persistErr
	})
	return row, err
}

func interventionReportError(err error) (int, string) {
	var rejection service.InterventionError
	var postgres *pgconn.PgError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "daemon_timeout"
	case errors.Is(err, daemonws.ErrVscreenStale):
		return http.StatusConflict, "stale_generation"
	case errors.Is(err, daemonws.ErrVscreenCapacity):
		return http.StatusTooManyRequests, "request_capacity"
	case errors.Is(err, daemonws.ErrVscreenUnavailable), errors.Is(err, context.Canceled):
		return http.StatusServiceUnavailable, "daemon_unavailable"
	case errors.Is(err, pgx.ErrNoRows):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, protocol.ErrInvalidVscreenContract):
		return http.StatusBadRequest, "invalid_report"
	case errors.As(err, &postgres) && postgres.Code == "23505":
		return http.StatusConflict, "pending_conflict"
	case errors.As(err, &rejection):
		switch rejection {
		case "permission_denied":
			return http.StatusForbidden, string(rejection)
		case "invalid_report":
			return http.StatusBadRequest, string(rejection)
		case "source_mismatch", "source_not_stopped", "invalid_transition", "stale_generation", "report_replayed":
			return http.StatusConflict, string(rejection)
		}
	}
	return http.StatusInternalServerError, "intervention_failed"
}
