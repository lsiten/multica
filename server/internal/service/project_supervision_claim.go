package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Claims already own an agent row. The nonblocking project fence avoids
// waiting behind a controller that needs that agent to release ready work.
func (s *TaskService) checkProjectAdmission(ctx context.Context, q *db.Queries, task db.AgentTaskQueue) error {
	projectID, err := q.GetAgentTaskProjectID(ctx, task.ID)
	if err != nil {
		return err
	}
	if !projectID.Valid {
		return nil
	}
	agent, err := q.GetAgent(ctx, task.AgentID)
	if err != nil {
		return err
	}
	if _, err := q.TryLockSupervisionWorkspace(ctx, agent.WorkspaceID); err != nil {
		return ErrProjectExecutionCapacity
	}
	locked, err := q.TryProjectSupervisionLock(ctx, util.UUIDToString(projectID))
	if err != nil {
		return err
	}
	if !locked {
		return ErrProjectExecutionCapacity
	}
	row, err := q.LockProjectSupervision(ctx, db.LockProjectSupervisionParams{ProjectID: projectID, WorkspaceID: agent.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.Enabled {
		project, err := q.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: agent.WorkspaceID})
		if err != nil {
			return err
		}
		if project.Status != "in_progress" {
			return ErrProjectExecutionCapacity
		}
		runtime, err := q.GetSupervisionAgentRuntime(ctx, db.GetSupervisionAgentRuntimeParams{ID: task.RuntimeID, WorkspaceID: agent.WorkspaceID})
		if err != nil {
			return err
		}
		var metadata struct {
			ExecutionSlots int `json:"execution_slots"`
		}
		if json.Unmarshal(runtime.Metadata, &metadata) == nil && metadata.ExecutionSlots > 0 {
			key := util.UUIDToString(task.RuntimeID)
			if runtime.DaemonID.Valid {
				key = runtime.DaemonID.String + ":" + util.UUIDToString(runtime.OwnerID)
			}
			locked, err := q.TryProjectSupervisionLock(ctx, "runtime:"+key)
			if err != nil {
				return err
			}
			if !locked {
				return ErrProjectExecutionCapacity
			}
			count, err := q.CountDaemonSupervisionReservations(ctx, db.CountDaemonSupervisionReservationsParams{ID: task.RuntimeID, Statuses: []string{"dispatched", "running", "waiting_local_directory"}, ExcludeID: task.ID})
			if err != nil {
				return err
			}
			if count >= int64(metadata.ExecutionSlots) {
				return ErrProjectExecutionCapacity
			}
		}
	}
	if coordination, ok := ProjectCoordination(task); ok {
		project, err := q.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: agent.WorkspaceID})
		if err != nil {
			return err
		}
		if !row.Enabled || coordination.PolicyRevision != row.Revision || project.LeadID != task.AgentID || project.LeadType.String != "agent" || coordination.WorkspaceID != util.UUIDToString(agent.WorkspaceID) {
			return ErrProjectSupervisionForbidden
		}
		member, err := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: row.ConfiguredBy, WorkspaceID: agent.WorkspaceID})
		if err != nil || (member.Role != "owner" && member.Role != "admin") || !CanMemberInvokeAgent(ctx, q, agent, row.ConfiguredBy, agent.WorkspaceID) {
			return ErrProjectSupervisionForbidden
		}
		return s.validateTaskExecutionScope(ctx, agent.WorkspaceID, agent.ID, projectID, task.SquadID)
	}
	if !row.Enabled {
		return nil
	}
	config, err := supervisionConfig(row.Config)
	if err != nil {
		return err
	}
	count, err := q.CountProjectExecutionRuns(ctx, db.CountProjectExecutionRunsParams{ProjectID: projectID, WorkspaceID: agent.WorkspaceID, Statuses: []string{"dispatched", "running", "waiting_local_directory"}, ExcludeID: task.ID})
	if err != nil {
		return err
	}
	if count >= int64(config.MaxInFlight) {
		return ErrProjectExecutionCapacity
	}
	return nil
}
