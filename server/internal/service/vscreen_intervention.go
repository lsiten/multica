package service

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// InterventionError is a stable rejection reason at the HTTP boundary.
type InterventionError string

func (e InterventionError) Error() string { return string(e) }

func validInterventionTransition(from, to protocol.VscreenInterventionState) bool {
	if from == to {
		return true
	}
	switch from {
	case protocol.VscreenInterventionAwaitingTakeover:
		return to == protocol.VscreenInterventionHuman || to == protocol.VscreenInterventionCancelled || to == protocol.VscreenInterventionStale
	case protocol.VscreenInterventionHuman:
		return to == protocol.VscreenInterventionReadyToContinue || to == protocol.VscreenInterventionCancelled || to == protocol.VscreenInterventionStale
	case protocol.VscreenInterventionReadyToContinue:
		return to == protocol.VscreenInterventionCancelled || to == protocol.VscreenInterventionStale
	default:
		return false
	}
}

// ReportVscreenIntervention persists a report already authenticated against the current daemon connection.
func (s *TaskService) ReportVscreenIntervention(ctx context.Context, report protocol.VscreenIntervention) (db.RuntimeVscreenIntervention, error) {
	var empty db.RuntimeVscreenIntervention
	if err := report.Validate(); err != nil {
		return empty, err
	}
	if report.Epoch.GeometryRevision > math.MaxInt64 || report.State == protocol.VscreenInterventionContinued || report.HumanSummary != "" {
		return empty, InterventionError("invalid_report")
	}
	ids := make([]pgtype.UUID, 5)
	for n, raw := range []string{report.InterventionID, report.WorkspaceID, report.RuntimeID, report.AgentID, report.SourceTaskID} {
		id, err := util.ParseUUID(raw)
		if err != nil {
			return empty, InterventionError("invalid_report")
		}
		ids[n] = id
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	if _, err = q.LockVscreenWorkspace(ctx, ids[1]); err != nil {
		return empty, err
	}
	rt, err := q.LockAgentRuntime(ctx, ids[2])
	if err != nil {
		return empty, err
	}
	agent, err := q.GetAgentForClaimUpdate(ctx, ids[3])
	if err != nil {
		return empty, err
	}
	source, err := q.LockVscreenSourceTask(ctx, ids[4])
	if err != nil {
		return empty, err
	}
	if rt.WorkspaceID != ids[1] || agent.WorkspaceID != ids[1] || agent.RuntimeID != rt.ID || source.RuntimeID != rt.ID || source.AgentID != agent.ID || agent.ArchivedAt.Valid {
		return empty, InterventionError("source_mismatch")
	}
	if source.Status != "failed" || !source.CompletedAt.Valid || source.FailureReason.String != string(taskfailure.ReasonGUIHumanIntervention) {
		return empty, InterventionError("source_not_stopped")
	}
	if _, err = s.interventionScope(ctx, q, source, ids[1]); err != nil {
		return empty, err
	}
	row, err := q.LockVscreenIntervention(ctx, db.LockVscreenInterventionParams{ID: ids[0], WorkspaceID: ids[1]})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if report.State != protocol.VscreenInterventionAwaitingTakeover || report.ReturnReceiptID != "" {
			return empty, InterventionError("invalid_transition")
		}
		row, err = q.CreateVscreenIntervention(ctx, db.CreateVscreenInterventionParams{ID: ids[0], WorkspaceID: ids[1], RuntimeID: ids[2], AgentID: ids[3], SourceTaskID: ids[4], Reason: string(report.Reason), NativeEpoch: report.Epoch.NativeEpoch, DisplayGeneration: report.Epoch.DisplayGeneration, GeometryRevision: int64(report.Epoch.GeometryRevision), LastActionID: report.LastActionID, CreatedByUserID: rt.OwnerID})
	case err != nil:
		return empty, err
	default:
		if row.RuntimeID != rt.ID || row.AgentID != agent.ID || row.SourceTaskID != source.ID || row.Reason != string(report.Reason) || row.NativeEpoch != report.Epoch.NativeEpoch || row.DisplayGeneration != report.Epoch.DisplayGeneration || int64(report.Epoch.GeometryRevision) < row.GeometryRevision {
			return empty, InterventionError("stale_generation")
		}
		if !validInterventionTransition(protocol.VscreenInterventionState(row.State), report.State) {
			return empty, InterventionError("invalid_transition")
		}
		if row.State == string(report.State) {
			if row.ReturnReceiptID != report.ReturnReceiptID || row.LastActionID != report.LastActionID || row.GeometryRevision != int64(report.Epoch.GeometryRevision) {
				return empty, InterventionError("report_replayed")
			}
		} else {
			row, err = q.UpdateVscreenIntervention(ctx, db.UpdateVscreenInterventionParams{ID: row.ID, WorkspaceID: row.WorkspaceID, State: string(report.State), ReturnReceiptID: report.ReturnReceiptID, LastActionID: report.LastActionID, GeometryRevision: int64(report.Epoch.GeometryRevision)})
		}
	}
	if err != nil {
		return empty, fmt.Errorf("persist intervention: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return row, nil
}
