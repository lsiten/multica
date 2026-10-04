package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type ProjectSupervisionService struct{ Tasks *TaskService }

type ProjectSupervisionView struct {
	ProjectID       string                     `json:"project_id"`
	WorkspaceID     string                     `json:"workspace_id"`
	Enabled         bool                       `json:"enabled"`
	Config          ProjectSupervisionConfig   `json:"config"`
	Revision        int64                      `json:"revision"`
	DirtyVersion    int64                      `json:"dirty_version"`
	HandledVersion  int64                      `json:"handled_version"`
	Snapshot        ProjectSupervisionSnapshot `json:"snapshot"`
	LastReason      string                     `json:"last_reason"`
	NoProgressCount int32                      `json:"no_progress_count"`
	LastResult      json.RawMessage            `json:"last_result"`
	LastTaskID      *string                    `json:"last_task_id"`
	LastTaskStatus  string                     `json:"last_task_status"`
	CheckedVersion  int64                      `json:"checked_version"`
	NextCheckAt     *string                    `json:"next_check_at"`
	LastCheckedAt   *string                    `json:"last_checked_at"`
}

func (s *ProjectSupervisionService) View(ctx context.Context, project db.Project) (ProjectSupervisionView, error) {
	row, err := s.Tasks.Queries.GetProjectSupervision(ctx, db.GetProjectSupervisionParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		row = db.ProjectSupervision{ProjectID: project.ID, WorkspaceID: project.WorkspaceID, Config: []byte(`{}`), LastResult: []byte(`{}`)}
	} else if err != nil {
		return ProjectSupervisionView{}, err
	}
	config, err := supervisionConfig(row.Config)
	if err != nil {
		return ProjectSupervisionView{}, err
	}
	snapshot, err := s.snapshot(ctx, s.Tasks.Queries, project, config, row.ConfiguredBy)
	if err != nil {
		return ProjectSupervisionView{}, err
	}
	view := ProjectSupervisionView{ProjectID: util.UUIDToString(project.ID), WorkspaceID: util.UUIDToString(project.WorkspaceID), Enabled: row.Enabled, Config: config, Revision: row.Revision, DirtyVersion: row.DirtyVersion, HandledVersion: row.HandledVersion, Snapshot: snapshot, LastReason: row.LastReason, NoProgressCount: row.NoProgressCount, LastResult: row.LastResult, LastTaskID: util.UUIDToPtr(row.LastTaskID), NextCheckAt: util.TimestampToPtr(row.NextCheckAt), LastCheckedAt: util.TimestampToPtr(row.LastCheckedAt)}
	if row.LastTaskID.Valid {
		task, e := s.Tasks.Queries.GetAgentTask(ctx, row.LastTaskID)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return view, e
		}
		if e == nil {
			view.LastTaskStatus = task.Status
			if coordination, ok := ProjectCoordination(task); ok {
				view.CheckedVersion = coordination.CheckedVersion
			}
		}
	}
	return view, nil
}

func (s *ProjectSupervisionService) Save(ctx context.Context, project db.Project, user pgtype.UUID, enabled bool, config ProjectSupervisionConfig, revision int64) error {
	if err := config.Validate(); err != nil {
		return err
	}
	for _, key := range config.ReadyStatuses {
		status, err := s.Tasks.Queries.GetIssueStatusEntryByKey(ctx, db.GetIssueStatusEntryByKeyParams{WorkspaceID: project.WorkspaceID, Key: key})
		if err != nil || status.ArchivedAt.Valid || status.Category != "unstarted" {
			return errors.New("ready statuses must be active unstarted statuses")
		}
	}
	if enabled && project.LeadType.String == "agent" {
		agent, err := s.Tasks.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: project.LeadID, WorkspaceID: project.WorkspaceID})
		if err != nil {
			return err
		}
		if !CanMemberInvokeAgent(ctx, s.Tasks.Queries, agent, user, project.WorkspaceID) {
			return ErrProjectSupervisionForbidden
		}
	}
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := s.Tasks.Queries.WithTx(tx)
	if err := supervisionLock(ctx, q, project.ID, project.WorkspaceID); err != nil {
		return err
	}
	if _, err := q.LockProjectForDelete(ctx, db.LockProjectForDeleteParams{ID: project.ID, WorkspaceID: project.WorkspaceID}); err != nil {
		return err
	}
	fresh, err := q.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		return err
	}
	project = fresh
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	var saved db.ProjectSupervision
	if revision == 0 {
		saved, err = q.CreateProjectSupervision(ctx, db.CreateProjectSupervisionParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID, ConfiguredBy: user, Config: raw, Enabled: enabled})
	} else {
		saved, err = q.SaveProjectSupervision(ctx, db.SaveProjectSupervisionParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID, ConfiguredBy: user, Config: raw, Enabled: enabled, Revision: revision})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrProjectSupervisionConflict
	}
	if err != nil {
		return err
	}
	if _, err = q.CancelQueuedProjectCoordination(ctx, util.UUIDToString(project.ID)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if saved.LastTaskID.Valid {
		task, loadErr := s.Tasks.Queries.GetAgentTask(ctx, saved.LastTaskID)
		if loadErr == nil && supervisionTaskActive(task.Status) {
			if _, cancelErr := s.Tasks.CancelTaskWithReason(ctx, task.ID, "project supervision policy changed", "scope_changed"); cancelErr != nil {
				slog.Warn("cancel superseded project coordination", "task_id", util.UUIDToString(task.ID), "error", cancelErr)
			}
		}
	}
	s.publish(project)
	return nil
}

func (s *ProjectSupervisionService) CheckNow(ctx context.Context, project db.Project) error {
	if err := s.Tasks.Queries.ManualProjectSupervisionCheck(ctx, db.ManualProjectSupervisionCheckParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID}); err != nil {
		return err
	}
	row, err := s.Tasks.Queries.GetProjectSupervision(ctx, db.GetProjectSupervisionParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		return err
	}
	if !row.Enabled {
		return errors.New("project supervision is disabled")
	}
	return s.check(ctx, row)
}

// Listen records durable dirty versions after domain commits. The periodic scan
// also covers a crash between a business write and event publication.
func (s *ProjectSupervisionService) Listen() {
	if s.Tasks.Bus == nil {
		return
	}
	s.Tasks.Bus.SubscribeAll(func(event events.Event) {
		switch event.Type {
		case protocol.EventIssueCreated, protocol.EventIssueUpdated, protocol.EventIssueDeleted, protocol.EventTaskCompleted, protocol.EventTaskFailed, protocol.EventTaskCancelled, protocol.EventProjectUpdated, protocol.EventDaemonRegister:
		default:
			if !strings.HasPrefix(event.Type, "agent:") && !strings.HasPrefix(event.Type, "squad:") && !strings.HasPrefix(event.Type, "member:") {
				return
			}
		}
		ws, err := util.ParseUUID(event.WorkspaceID)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		raw, _ := json.Marshal(event.Payload)
		var payload struct {
			TaskID    string `json:"task_id"`
			IssueID   string `json:"issue_id"`
			ProjectID string `json:"project_id"`
			Issue     struct {
				ID        string `json:"id"`
				ProjectID string `json:"project_id"`
			} `json:"issue"`
			Project struct {
				ID string `json:"id"`
			} `json:"project"`
		}
		if err = json.Unmarshal(raw, &payload); err != nil {
			return
		}
		projectRaw := payload.ProjectID
		if payload.Project.ID != "" {
			projectRaw = payload.Project.ID
		}
		if payload.Issue.ProjectID != "" {
			projectRaw = payload.Issue.ProjectID
		}
		if strings.HasPrefix(event.Type, "task:") {
			if err = s.Tasks.Queries.RequestWorkspaceSupervisionCapacityChecks(ctx, ws); err != nil {
				slog.Warn("project capacity check event failed", "error", err)
			}
			if id, e := util.ParseUUID(payload.TaskID); e == nil {
				if task, e := s.Tasks.Queries.GetAgentTask(ctx, id); e == nil {
					if coord, ok := ProjectCoordination(task); ok {
						if projectID, e := util.ParseUUID(coord.ProjectID); e == nil {
							_ = s.Tasks.Queries.RequestProjectSupervisionCheck(ctx, db.RequestProjectSupervisionCheckParams{ProjectID: projectID, WorkspaceID: ws})
						}
						return
					}
					if id, e := s.Tasks.Queries.GetAgentTaskProjectID(ctx, task.ID); e == nil && id.Valid {
						projectRaw = util.UUIDToString(id)
					}
				}
			}
		}
		if projectRaw == "" && strings.HasPrefix(event.Type, "issue:") {
			issueRaw := payload.IssueID
			if issueRaw == "" {
				issueRaw = payload.Issue.ID
			}
			if id, e := util.ParseUUID(issueRaw); e == nil {
				if issue, e := s.Tasks.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: id, WorkspaceID: ws}); e == nil && issue.ProjectID.Valid {
					projectRaw = util.UUIDToString(issue.ProjectID)
				}
			}
		}
		if projectID, e := util.ParseUUID(projectRaw); e == nil {
			if event.Type == protocol.EventProjectUpdated {
				_, _ = s.Tasks.Queries.CancelObsoleteProjectCoordination(ctx, db.CancelObsoleteProjectCoordinationParams{ID: projectID, WorkspaceID: ws})
			}
			err = s.Tasks.Queries.MarkProjectSupervisionDirty(ctx, db.MarkProjectSupervisionDirtyParams{ProjectID: projectID, WorkspaceID: ws})
			if err != nil {
				slog.Warn("project supervision dirty event failed", "event", event.Type, "error", err)
			}
			return
		}
		if strings.HasPrefix(event.Type, "task:") || event.Type == protocol.EventIssueCreated || event.Type == protocol.EventIssueUpdated {
			return
		}
		if err = s.Tasks.Queries.MarkWorkspaceSupervisionDirty(ctx, ws); err != nil {
			slog.Warn("project supervision dirty event failed", "event", event.Type, "error", err)
		}
	})
}

func (s *ProjectSupervisionService) Tick(ctx context.Context) error {
	rows, err := s.Tasks.Queries.ListDueProjectSupervisions(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = s.check(ctx, row); err != nil {
			slog.Warn("project supervision check failed", "project_id", util.UUIDToString(row.ProjectID), "error", err)
		}
	}
	return nil
}

func (s *ProjectSupervisionService) check(ctx context.Context, previous db.ProjectSupervision) error {
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := s.Tasks.Queries.WithTx(tx)
	if err := supervisionLock(ctx, q, previous.ProjectID, previous.WorkspaceID); err != nil {
		return err
	}
	row, err := q.LockProjectSupervision(ctx, db.LockProjectSupervisionParams{ProjectID: previous.ProjectID, WorkspaceID: previous.WorkspaceID})
	if err != nil {
		return err
	}
	if !row.Enabled {
		return nil
	}
	project, err := q.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: row.ProjectID, WorkspaceID: row.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		if err = q.DeleteProjectSupervision(ctx, db.DeleteProjectSupervisionParams{ProjectID: row.ProjectID, WorkspaceID: row.WorkspaceID}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if _, err = q.LockProjectForDelete(ctx, db.LockProjectForDeleteParams{ID: project.ID, WorkspaceID: project.WorkspaceID}); err != nil {
		return err
	}
	project, err = q.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		return err
	}
	config, err := supervisionConfig(row.Config)
	if err != nil {
		return err
	}
	snapshot, err := s.snapshot(ctx, q, project, config, row.ConfiguredBy)
	if err != nil {
		return err
	}
	next := time.Now().Add(time.Duration(config.ScanIntervalSeconds) * time.Second)
	reason := "healthy"
	taskID := row.LastTaskID
	store := func() error {
		_, e := q.StoreProjectSupervisionCheck(ctx, db.StoreProjectSupervisionCheckParams{ProjectID: row.ProjectID, WorkspaceID: row.WorkspaceID, NextCheckAt: pgtype.Timestamptz{Time: next, Valid: true}, LastReason: reason, LastTaskID: taskID, LastFingerprint: snapshot.Fingerprint})
		if e != nil {
			return e
		}
		if e = tx.Commit(ctx); e != nil {
			return e
		}
		s.publish(project)
		return nil
	}
	if project.Status == "completed" || project.Status == "cancelled" || project.Status == "paused" || project.Status == "planned" {
		reason = "project_ended"
		return store()
	}
	if row.LastFingerprint != "" && row.LastFingerprint != snapshot.Fingerprint && row.NoProgressCount > 0 {
		row, err = q.StoreProjectSupervisionResult(ctx, db.StoreProjectSupervisionResultParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID, HandledVersion: row.HandledVersion, LastResult: row.LastResult, NoProgressCount: 0, LastReason: row.LastReason})
		if err != nil {
			return err
		}
	}
	if taskID.Valid {
		task, e := q.GetAgentTask(ctx, taskID)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if e == nil && supervisionTaskActive(task.Status) {
			coord, _ := ProjectCoordination(task)
			if (task.Status == "queued" || task.Status == "deferred") && (project.LeadType.String != "agent" || task.AgentID != project.LeadID || coord.PolicyRevision != row.Revision) {
				if _, err = q.CancelQueuedProjectCoordination(ctx, util.UUIDToString(project.ID)); err != nil {
					return err
				}
				task.Status = "cancelled"
			} else {
				if task.Status == "queued" || task.Status == "deferred" {
					runtime, err := q.GetSupervisionAgentRuntime(ctx, db.GetSupervisionAgentRuntimeParams{ID: task.RuntimeID, WorkspaceID: project.WorkspaceID})
					if errors.Is(err, pgx.ErrNoRows) {
						reason = "runtime_offline"
						return store()
					}
					if err != nil {
						return err
					}
					seen := runtime.LastSeenAt
					if !seen.Valid {
						seen = runtime.UpdatedAt
					}
					if runtime.Status != "online" || !seen.Valid || time.Since(seen.Time) > time.Duration(RuntimeClaimFreshnessSeconds)*time.Second {
						reason = "runtime_offline"
						return store()
					}
					var metadata struct {
						Capabilities []string `json:"capabilities"`
					}
					if err = json.Unmarshal(runtime.Metadata, &metadata); err != nil {
						return err
					}
					supported := false
					for _, capability := range metadata.Capabilities {
						if capability == protocol.DaemonCapabilityProjectSupervisionV1 {
							supported = true
						}
					}
					if !supported {
						reason = "runtime_upgrade_required"
						return store()
					}
				}
				if task.Status == "queued" && coord.CheckedVersion < row.DirtyVersion {
					coord.CheckedVersion = row.DirtyVersion
					coord.Fingerprint = snapshot.Fingerprint
					coord.Prompt = supervisionPrompt(project, snapshot, config, row.DirtyVersion)
					raw, err := json.Marshal(coord)
					if err != nil {
						return err
					}
					if _, err = q.MergeQueuedProjectCoordination(ctx, db.MergeQueuedProjectCoordinationParams{ID: task.ID, Context: raw}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
						return err
					}
				}
				reason = "coordination_active"
				return store()
			}
		}
		if e == nil {
			coord, ok := ProjectCoordination(task)
			var last struct {
				TaskID string `json:"task_id"`
			}
			if err = json.Unmarshal(row.LastResult, &last); err != nil {
				return err
			}
			if ok && last.TaskID != util.UUIDToString(task.ID) && coord.PolicyRevision == row.Revision && task.AgentID == project.LeadID {
				count := row.NoProgressCount + 1
				raw, _ := json.Marshal(map[string]any{"decision": "blocked", "summary": "Coordination ended without a verified report", "task_id": util.UUIDToString(task.ID), "checked_version": coord.CheckedVersion})
				row, err = q.StoreProjectSupervisionResult(ctx, db.StoreProjectSupervisionResultParams{ProjectID: row.ProjectID, WorkspaceID: row.WorkspaceID, HandledVersion: coord.CheckedVersion, LastResult: raw, NoProgressCount: count, LastReason: "missing_report"})
				if err != nil {
					return err
				}
			}
		}
	}
	if snapshot.Actionable == 0 {
		reason = "no_actionable_work"
		if snapshot.Counts.Blocked > 0 {
			reason = "dependency_wait"
			for _, item := range snapshot.Issues {
				if item.Category == "blocked" && item.Reason != "dependency" {
					reason = item.Reason
					break
				}
			}
		}
		_, err = q.StoreProjectSupervisionResult(ctx, db.StoreProjectSupervisionResultParams{ProjectID: row.ProjectID, WorkspaceID: row.WorkspaceID, HandledVersion: row.DirtyVersion, LastResult: row.LastResult, NoProgressCount: row.NoProgressCount, LastReason: reason})
		if err != nil {
			return err
		}
		return store()
	}
	if project.LeadType.String != "agent" || !project.LeadID.Valid {
		reason = "no_agent_lead"
		return store()
	}
	agent, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: project.LeadID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		return err
	}
	// Release deterministic work before competing for a coordinator slot.
	queued, err := s.releaseReady(ctx, q, project, row, config, snapshot, pgtype.UUID{})
	if err != nil {
		return err
	}
	finish := func() error {
		if err := store(); err != nil {
			return err
		}
		for _, run := range queued {
			s.Tasks.NotifyTaskEnqueued(ctx, run)
		}
		s.publishIssueChanges(ctx, snapshot, queued, nil)
		return nil
	}
	if row.NoProgressCount >= int32(config.NoProgressLimit) && row.LastFingerprint == snapshot.Fingerprint {
		reason = "needs_human"
		if len(queued) > 0 {
			reason = "work_released"
		}
		return finish()
	}
	if snapshot.Counts.Unassigned == 0 && snapshot.Counts.Review == 0 && snapshot.Counts.Stalled == 0 && snapshot.Actionable <= snapshot.Counts.Ready {
		reason = "capacity_wait"
		if len(queued) > 0 {
			reason = "work_released"
		}
		return finish()
	}
	if reason, err = s.agentReady(ctx, q, project, agent, row.ConfiguredBy, false); err != nil {
		return err
	}
	if reason != "" {
		return finish()
	}
	agent, err = q.TryLockSupervisionAgent(ctx, db.TryLockSupervisionAgentParams{ID: agent.ID, WorkspaceID: project.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		reason = "lead_capacity"
		return finish()
	}
	if err != nil {
		return err
	}
	pending, err := q.CountPendingCoordinationRuns(ctx, agent.ID)
	if err != nil {
		return err
	}
	if pending >= int64(agent.MaxConcurrentTasks) {
		reason = "lead_capacity"
		return finish()
	}
	prompt := supervisionPrompt(project, snapshot, config, row.DirtyVersion)
	contextData, _ := json.Marshal(ProjectCoordinationContext{Type: ProjectSupervisionContextType, ProjectID: util.UUIDToString(project.ID), WorkspaceID: util.UUIDToString(project.WorkspaceID), RequesterID: util.UUIDToString(row.ConfiguredBy), PolicyRevision: row.Revision, CheckedVersion: row.DirtyVersion, Fingerprint: snapshot.Fingerprint, Prompt: prompt})
	overlay := s.Tasks.buildRuntimeMCPOverlay(ctx, row.ConfiguredBy, agent)
	task, err := q.CreateProjectCoordinationTask(ctx, db.CreateProjectCoordinationTaskParams{ID: dbid.NewV7(), AgentID: agent.ID, RuntimeID: agent.RuntimeID, Context: contextData, OriginatorUserID: row.ConfiguredBy, TriggerEvidenceRefID: project.ID, RuntimeMcpOverlay: overlay.Overlay, RuntimeConnectedApps: overlay.ConnectedApps})
	if err != nil {
		return err
	}
	taskID = task.ID
	reason = "coordination_queued"
	if err = store(); err != nil {
		return err
	}
	for _, run := range queued {
		s.Tasks.NotifyTaskEnqueued(ctx, run)
	}
	s.publishIssueChanges(ctx, snapshot, queued, nil)
	s.Tasks.NotifyTaskEnqueued(ctx, task)
	s.publish(project)
	return nil
}

func supervisionTaskActive(status string) bool {
	switch status {
	case "queued", "deferred", "dispatched", "running", "waiting_local_directory":
		return true
	}
	return false
}
func supervisionPrompt(project db.Project, snapshot ProjectSupervisionSnapshot, config ProjectSupervisionConfig, version int64) string {
	brief, _ := json.Marshal(snapshot.Counts)
	return fmt.Sprintf("Project %s (%s). Inspect this project's supervision snapshot. Resolve missing assignments, review and stalls using project supervision actions only. For failed or blocked work, inspect the latest execution error and issue discussion, diagnose the cause with the responsible agent, and give concrete repair instructions before retrying. Continue independent ready work while the affected task is repaired; a task failure is not project completion. Do not repeat an unchanged failing attempt. Request human help only for a concrete permission, credential, resource, or unresolved decision, with evidence and a specific next action. Do not create a patrol issue or modify another project. Make at most %d actions, report action/wait/blocked/needs_human with checked_version %d and current task ID, then end this short coordination run. Never sleep or poll. Pending facts are retained by the server. Snapshot: %s", project.Title, util.UUIDToString(project.ID), config.BatchSize, version, brief)
}
func (s *ProjectSupervisionService) publish(project db.Project) {
	if s.Tasks.Bus != nil {
		s.Tasks.Bus.Publish(events.Event{Type: protocol.EventProjectSupervisionUpdated, WorkspaceID: util.UUIDToString(project.WorkspaceID), ActorType: "system", Payload: map[string]any{"project_id": util.UUIDToString(project.ID)}})
	}
}
