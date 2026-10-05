package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/attribution"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func (s *ProjectSupervisionService) snapshot(ctx context.Context, q *db.Queries, project db.Project, config ProjectSupervisionConfig, user pgtype.UUID) (ProjectSupervisionSnapshot, error) {
	rows, err := q.ListSupervisionIssues(ctx, db.ListSupervisionIssuesParams{WorkspaceID: project.WorkspaceID, ProjectID: project.ID})
	if err != nil {
		return ProjectSupervisionSnapshot{}, err
	}
	ws, err := q.GetWorkspace(ctx, project.WorkspaceID)
	if err != nil {
		return ProjectSupervisionSnapshot{}, err
	}
	result := buildSupervisionSnapshot(rows, config, ws.IssuePrefix, time.Now())
	byID := map[string]db.ListSupervisionIssuesRow{}
	children := map[string][]db.ListSupervisionIssuesRow{}
	for _, row := range rows {
		byID[util.UUIDToString(row.ID)] = row
		if row.ParentIssueID.Valid {
			key := util.UUIDToString(row.ParentIssueID)
			children[key] = append(children[key], row)
		}
	}
	if config.AutoAdvance {
		for index := range result.Issues {
			item := &result.Issues[index]
			row := byID[item.ID]
			if item.Category != "paused" || row.Status != "backlog" || !row.Stage.Valid || !row.ParentIssueID.Valid {
				continue
			}
			state := stageSnapshotState(row, byID, children)
			if state == "cancelled_stage_decision" {
				item.Category = "blocked"
				item.Reason = state
				result.Counts.Paused--
				result.Counts.Blocked++
				result.Actionable++
			}
			if state == "stage_ready" {
				item.Category = "ready"
				item.Reason = "stage_ready"
				result.Counts.Paused--
				result.Counts.Ready++
				result.Actionable++
			}
		}
	}
	reasons := map[string]string{}
	for index := range result.Issues {
		item := &result.Issues[index]
		if item.Category != "ready" {
			continue
		}
		key := item.AssigneeType + ":" + valueOrEmpty(item.AssigneeID)
		reason, exists := reasons[key]
		if !exists {
			id, e := util.ParseUUID(valueOrEmpty(item.AssigneeID))
			if e != nil {
				reason = "invalid_assignee"
			} else {
				agent, squad, e := s.resolveAgent(ctx, q, project, item.AssigneeType, id)
				if e != nil {
					reason = "assignee_unavailable"
				} else if user.Valid {
					taskSvc := &TaskService{Queries: q}
					if e = taskSvc.validateTaskExecutionScope(ctx, project.WorkspaceID, agent.ID, project.ID, squad); e != nil {
						reason = "scope_conflict"
					} else {
						reason, e = s.agentReady(ctx, q, project, agent, user, true)
						if e != nil {
							return result, e
						}
					}
				}
			}
			reasons[key] = reason
		}
		if reason != "" {
			item.Category = "blocked"
			item.Reason = reason
			result.Counts.Ready--
			result.Counts.Blocked++
			if reason == "agent_capacity" || reason == "runtime_capacity" || reason == "runtime_offline" {
				result.Actionable--
			}
		}
	}
	facts := []string{result.Fingerprint, project.Status, project.LeadType.String + ":" + util.UUIDToString(project.LeadID)}
	for _, item := range result.Issues {
		facts = append(facts, item.ID+":"+item.Category+":"+item.Reason)
	}
	sort.Strings(facts)
	payload, err := json.Marshal(facts)
	if err != nil {
		return result, err
	}
	hash := sha256.Sum256(payload)
	result.Fingerprint = hex.EncodeToString(hash[:])
	return result, nil
}
func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stageSnapshotState(issue db.ListSupervisionIssuesRow, byID map[string]db.ListSupervisionIssuesRow, children map[string][]db.ListSupervisionIssuesRow) string {
	if issue.Status != "backlog" || !issue.Stage.Valid || !issue.ParentIssueID.Valid {
		return ""
	}
	key := util.UUIDToString(issue.ParentIssueID)
	parent, ok := byID[key]
	if !ok || parent.Status == "backlog" || parent.Status == "triage" || parent.Status == "done" || parent.Status == "cancelled" || parent.StatusCategory == "done" || parent.StatusCategory == "closed" {
		return ""
	}
	cancelled := false
	for _, child := range children[key] {
		if !child.Stage.Valid || child.Stage.Int32 >= issue.Stage.Int32 {
			continue
		}
		if child.Status == "cancelled" {
			cancelled = true
			continue
		}
		if child.Status != "done" && child.StatusCategory != "done" && child.StatusCategory != "closed" {
			return ""
		}
	}
	if cancelled {
		return "cancelled_stage_decision"
	}
	return "stage_ready"
}

func (s *ProjectSupervisionService) resolveAgent(ctx context.Context, q *db.Queries, project db.Project, kind string, id pgtype.UUID) (db.Agent, pgtype.UUID, error) {
	var squadID pgtype.UUID
	if kind == "squad" {
		squad, err := q.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{ID: id, WorkspaceID: project.WorkspaceID})
		if err != nil || squad.ArchivedAt.Valid {
			return db.Agent{}, squadID, errors.New("squad unavailable")
		}
		squadID = id
		id = squad.LeaderID
	} else if kind != "agent" {
		return db.Agent{}, squadID, errors.New("assignee requires human action")
	}
	agent, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: id, WorkspaceID: project.WorkspaceID})
	return agent, squadID, err
}

func (s *ProjectSupervisionService) agentReady(ctx context.Context, q *db.Queries, project db.Project, agent db.Agent, user pgtype.UUID, reserved bool) (string, error) {
	if agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
		return "agent_unavailable", nil
	}
	member, err := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: user, WorkspaceID: project.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "authority_revoked", nil
	}
	if err != nil {
		return "", err
	}
	if (member.Role != "owner" && member.Role != "admin") || !CanMemberInvokeAgent(ctx, q, agent, user, project.WorkspaceID) {
		return "permission_denied", nil
	}
	runtime, err := q.GetSupervisionAgentRuntime(ctx, db.GetSupervisionAgentRuntimeParams{ID: agent.RuntimeID, WorkspaceID: project.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "runtime_offline", nil
	}
	if err != nil {
		return "", err
	}
	fresh := runtime.LastSeenAt
	if !fresh.Valid {
		fresh = runtime.UpdatedAt
	}
	if runtime.Status != "online" || !fresh.Valid || time.Since(fresh.Time) > time.Duration(RuntimeClaimFreshnessSeconds)*time.Second {
		return "runtime_offline", nil
	}
	if runtime.Visibility == "private" && runtime.OwnerID.Valid && agent.OwnerID.Valid && runtime.OwnerID != agent.OwnerID {
		return "runtime_owner_mismatch", nil
	}
	var slots struct {
		ExecutionSlots int `json:"execution_slots"`
	}
	if err := json.Unmarshal(runtime.Metadata, &slots); err == nil && slots.ExecutionSlots > 0 {
		key := util.UUIDToString(agent.RuntimeID)
		if runtime.DaemonID.Valid {
			key = runtime.DaemonID.String + ":" + util.UUIDToString(runtime.OwnerID)
		}
		locked, err := q.TryProjectSupervisionLock(ctx, "runtime:"+key)
		if err != nil {
			return "", err
		}
		if !locked {
			return "runtime_capacity", nil
		}
		count, err := q.CountDaemonSupervisionReservations(ctx, db.CountDaemonSupervisionReservationsParams{ID: agent.RuntimeID, Statuses: []string{"queued", "deferred", "dispatched", "running", "waiting_local_directory"}})
		if err != nil {
			return "", err
		}
		if count >= int64(slots.ExecutionSlots) {
			return "runtime_capacity", nil
		}
	}
	if !reserved {
		var metadata struct {
			Capabilities []string `json:"capabilities"`
		}
		if err := json.Unmarshal(runtime.Metadata, &metadata); err != nil {
			return "runtime_upgrade_required", nil
		}
		found := false
		for _, capability := range metadata.Capabilities {
			if capability == "project-supervision-v1" {
				found = true
			}
		}
		if !found {
			return "runtime_upgrade_required", nil
		}
	}
	var count int64
	if reserved {
		count, err = q.CountAgentReservedRuns(ctx, agent.ID)
	} else {
		count, err = q.CountRunningTasks(ctx, agent.ID)
	}
	if err != nil {
		return "", err
	}
	if count >= int64(agent.MaxConcurrentTasks) {
		return "agent_capacity", nil
	}
	return "", nil
}

func (s *ProjectSupervisionService) stageReady(ctx context.Context, q *db.Queries, project db.Project, issue db.Issue) (bool, error) {
	if issue.Status != "backlog" || !issue.Stage.Valid || !issue.ParentIssueID.Valid {
		return false, nil
	}
	parent, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: issue.ParentIssueID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		return false, err
	}
	if parent.ProjectID != project.ID || parent.Status == "backlog" || parent.Status == "triage" || parent.Status == "done" || parent.Status == "cancelled" {
		return false, nil
	}
	parentStatus, err := q.GetIssueStatusEntryByKey(ctx, db.GetIssueStatusEntryByKeyParams{WorkspaceID: project.WorkspaceID, Key: parent.Status})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if parentStatus.Category == "done" || parentStatus.Category == "closed" {
		return false, nil
	}
	children, err := q.ListChildIssues(ctx, parent.ID)
	if err != nil {
		return false, err
	}
	for _, child := range children {
		if child.WorkspaceID != project.WorkspaceID || child.ProjectID != project.ID {
			return false, nil
		}
		if !child.Stage.Valid || child.Stage.Int32 >= issue.Stage.Int32 {
			continue
		}
		if child.Status == "cancelled" {
			return false, nil
		}
		category, e := q.GetIssueStatusEntryByKey(ctx, db.GetIssueStatusEntryByKeyParams{WorkspaceID: project.WorkspaceID, Key: child.Status})
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return false, e
		}
		if child.Status != "done" && category.Category != "done" && category.Category != "closed" {
			return false, nil
		}
	}
	return true, nil
}

func (s *ProjectSupervisionService) releaseReady(ctx context.Context, q *db.Queries, project db.Project, row db.ProjectSupervision, config ProjectSupervisionConfig, snapshot ProjectSupervisionSnapshot, parent pgtype.UUID) ([]db.AgentTaskQueue, error) {
	reserved, err := q.CountProjectExecutionRuns(ctx, db.CountProjectExecutionRunsParams{WorkspaceID: project.WorkspaceID, ProjectID: project.ID, Statuses: []string{"queued", "deferred", "dispatched", "running", "waiting_local_directory"}})
	if err != nil {
		return nil, err
	}
	available := min(config.BatchSize, config.MaxInFlight-int(reserved))
	if available <= 0 {
		return nil, nil
	}
	queued := []db.AgentTaskQueue{}
	blockedAssignees := map[string]bool{}
	ready := map[string]bool{}
	for _, status := range config.ReadyStatuses {
		ready[status] = true
	}
	for _, entry := range snapshot.Issues {
		if len(queued) >= available {
			break
		}
		if entry.ActiveRuns > 0 || entry.Category != "ready" {
			continue
		}
		key := entry.AssigneeType + ":" + valueOrEmpty(entry.AssigneeID)
		if blockedAssignees[key] {
			continue
		}
		id, err := util.ParseUUID(entry.ID)
		if err != nil {
			return nil, err
		}
		issue, err := q.LockIssueForDescriptionUpdate(ctx, db.LockIssueForDescriptionUpdateParams{ID: id, WorkspaceID: project.WorkspaceID})
		if err != nil {
			return nil, err
		}
		if issue.ProjectID != project.ID {
			continue
		}
		promote := false
		if issue.Status == "backlog" && config.AutoAdvance {
			promote, err = s.stageReady(ctx, q, project, issue)
			if err != nil {
				return nil, err
			}
		}
		if !ready[issue.Status] && !promote {
			continue
		}
		// Re-read dependency and duplicate-run facts after the issue lock.
		facts, err := q.GetSupervisionIssueEvidence(ctx, issue.ID)
		if err != nil {
			return nil, err
		}
		if facts.DependencyBlocked || facts.ActiveRuns > 0 {
			continue
		}
		agent, squad, err := s.resolveAgent(ctx, q, project, issue.AssigneeType.String, issue.AssigneeID)
		if err != nil {
			continue
		}
		agent, err = q.TryLockSupervisionAgent(ctx, db.TryLockSupervisionAgentParams{ID: agent.ID, WorkspaceID: project.WorkspaceID})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		txSvc := &TaskService{Queries: q}
		if err = txSvc.validateTaskExecutionScope(ctx, project.WorkspaceID, agent.ID, project.ID, squad); err != nil {
			continue
		}
		reason, err := s.agentReady(ctx, q, project, agent, row.ConfiguredBy, true)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			blockedAssignees[key] = true
			continue
		}
		if promote {
			issue, err = q.PromoteSupervisionIssue(ctx, db.PromoteSupervisionIssueParams{ID: issue.ID, WorkspaceID: project.WorkspaceID, ProjectID: project.ID, Revision: issue.Revision})
			if err != nil {
				return nil, err
			}
		}
		attr := attribution.DirectHumanRun(row.ConfiguredBy, attribution.EvidenceIssueAssignment, issue.ID)
		source, _, kind, ref := attributionCreateParams(attr)
		if parent.Valid {
			source = pgtype.Text{String: "delegation", Valid: true}
			kind = pgtype.Text{String: "project_supervision", Valid: true}
			ref = project.ID
		}
		overlay := s.Tasks.buildRuntimeMCPOverlay(ctx, row.ConfiguredBy, agent)
		task, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{ID: dbid.NewV7(), AgentID: agent.ID, RuntimeID: agent.RuntimeID, IssueID: issue.ID, Priority: priorityToInt(issue.Priority), SquadID: squad, IsLeaderTask: pgtype.Bool{Bool: squad.Valid, Valid: squad.Valid}, OriginatorUserID: row.ConfiguredBy, AccountableUserID: row.ConfiguredBy, OriginatorSource: source, DelegatedFromTaskID: parent, TriggerEvidenceKind: kind, TriggerEvidenceRefID: ref, RuntimeMcpOverlay: overlay.Overlay, RuntimeConnectedApps: overlay.ConnectedApps})
		if err != nil {
			return nil, err
		}
		queued = append(queued, task)
		count, err := q.CountAgentReservedRuns(ctx, agent.ID)
		if err != nil {
			return nil, err
		}
		if count >= int64(agent.MaxConcurrentTasks) {
			blockedAssignees[key] = true
		}
	}
	return queued, nil
}

func (s *ProjectSupervisionService) Apply(ctx context.Context, project db.Project, taskID pgtype.UUID, version int64, actions []ProjectSupervisionAction) (int, error) {
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	q := s.Tasks.Queries.WithTx(tx)
	if err := supervisionLock(ctx, q, project.ID, project.WorkspaceID); err != nil {
		return 0, err
	}
	row, task, config, err := s.authorizeRun(ctx, q, project, taskID, version)
	if err != nil {
		return 0, err
	}
	var reported struct {
		TaskID string `json:"task_id"`
	}
	if err = json.Unmarshal(row.LastResult, &reported); err != nil {
		return 0, err
	}
	if reported.TaskID == util.UUIDToString(task.ID) {
		return 0, ErrProjectSupervisionConflict
	}
	var progress struct {
		Verified  int `json:"verified_actions"`
		Requested int `json:"requested_actions"`
	}
	if err = json.Unmarshal(task.Context, &progress); err != nil {
		return 0, err
	}
	if len(actions) == 0 || len(actions)+progress.Requested > config.BatchSize {
		return 0, errors.New("coordination batch exceeds project policy")
	}
	changed := 0
	queuedActions := []db.AgentTaskQueue{}
	beforeIssues := map[string]db.Issue{}
	for _, action := range actions {
		id, e := util.ParseUUID(action.IssueID)
		if e != nil {
			return 0, e
		}
		issue, e := q.LockIssueForDescriptionUpdate(ctx, db.LockIssueForDescriptionUpdateParams{ID: id, WorkspaceID: project.WorkspaceID})
		if e != nil {
			return 0, e
		}
		if issue.ProjectID != project.ID || issue.Revision != action.Revision {
			return 0, ErrProjectSupervisionConflict
		}
		if _, exists := beforeIssues[action.IssueID]; !exists {
			beforeIssues[action.IssueID] = issue
		}
		switch action.Kind {
		case "retry", "delegate_review":
			if action.Reason == "" {
				return 0, errors.New("retry and review delegation require a reason")
			}
			evidence, err := q.GetSupervisionIssueEvidence(ctx, issue.ID)
			if err != nil {
				return 0, err
			}
			if evidence.ActiveRuns > 0 {
				return 0, ErrProjectExecutionCapacity
			}
			if evidence.DependencyBlocked {
				return 0, errors.New("dependencies must be resolved before execution")
			}
			if action.Kind == "retry" && (issue.Status != "in_progress" || evidence.LastRunStatus != "failed") {
				return 0, errors.New("retry requires failed in-progress work with no active run")
			}
			if action.Kind == "retry" && evidence.FailedSupervisionAttempts >= int32(config.NoProgressLimit) {
				return 0, errors.New("supervision retry limit reached; human decision required")
			}
			if action.Kind == "delegate_review" && (issue.Status != "in_review" || !evidence.HasDelivery) {
				return 0, errors.New("review delegation requires delivery evidence awaiting review")
			}
			if action.Kind == "delegate_review" && strings.HasPrefix(evidence.LastHandoff, "delegate_review:") {
				return 0, errors.New("review already delivered; human decision or new delivery required")
			}
			assignee, e := util.ParseUUID(action.AssigneeID)
			if e != nil {
				return 0, e
			}
			agent, squad, e := s.resolveAgent(ctx, q, project, action.AssigneeType, assignee)
			if e != nil {
				return 0, e
			}
			if action.Kind == "delegate_review" && agent.ID == issue.AssigneeID {
				return 0, errors.New("review must use an independent agent")
			}
			if action.Kind == "delegate_review" && issue.AssigneeID.Valid {
				owner, _, err := s.resolveAgent(ctx, q, project, issue.AssigneeType.String, issue.AssigneeID)
				if err == nil && owner.ID == agent.ID {
					return 0, errors.New("review must use an independent agent")
				}
			}
			if action.Kind == "retry" && assignee != issue.AssigneeID {
				return 0, errors.New("retry must preserve issue ownership")
			}
			agent, e = q.TryLockSupervisionAgent(ctx, db.TryLockSupervisionAgentParams{ID: agent.ID, WorkspaceID: project.WorkspaceID})
			if e != nil {
				return 0, ErrProjectExecutionCapacity
			}
			txSvc := &TaskService{Queries: q}
			if e = txSvc.validateTaskExecutionScope(ctx, project.WorkspaceID, agent.ID, project.ID, squad); e != nil {
				return 0, e
			}
			reason, e := s.agentReady(ctx, q, project, agent, row.ConfiguredBy, true)
			if e != nil {
				return 0, e
			}
			if reason != "" {
				return 0, ErrProjectExecutionCapacity
			}
			reserved, e := q.CountProjectExecutionRuns(ctx, db.CountProjectExecutionRunsParams{WorkspaceID: project.WorkspaceID, ProjectID: project.ID, Statuses: []string{"queued", "deferred", "dispatched", "running", "waiting_local_directory"}})
			if e != nil {
				return 0, e
			}
			if reserved >= int64(config.MaxInFlight) {
				return 0, ErrProjectExecutionCapacity
			}
			overlay := s.Tasks.buildRuntimeMCPOverlay(ctx, row.ConfiguredBy, agent)
			run, e := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{ID: dbid.NewV7(), AgentID: agent.ID, RuntimeID: agent.RuntimeID, IssueID: issue.ID, Priority: priorityToInt(issue.Priority), SquadID: squad, IsLeaderTask: pgtype.Bool{Bool: squad.Valid, Valid: squad.Valid}, ForceFreshSession: pgtype.Bool{Bool: true, Valid: true}, HandoffNote: pgtype.Text{String: action.Kind + ": " + action.Reason, Valid: true}, OriginatorUserID: row.ConfiguredBy, AccountableUserID: row.ConfiguredBy, OriginatorSource: pgtype.Text{String: "delegation", Valid: true}, DelegatedFromTaskID: task.ID, TriggerEvidenceKind: pgtype.Text{String: "project_supervision", Valid: true}, TriggerEvidenceRefID: project.ID, RuntimeMcpOverlay: overlay.Overlay, RuntimeConnectedApps: overlay.ConnectedApps})
			if e != nil {
				return 0, e
			}
			queuedActions = append(queuedActions, run)
			changed++
		case "assign":
			ready := false
			for _, key := range config.ReadyStatuses {
				if issue.Status == key {
					ready = true
				}
			}
			if issue.AssigneeID.Valid || !ready {
				return 0, errors.New("only unassigned ready work may be assigned by supervision")
			}
			assignee, e := util.ParseUUID(action.AssigneeID)
			if e != nil {
				return 0, e
			}
			agent, squad, e := s.resolveAgent(ctx, q, project, action.AssigneeType, assignee)
			if e != nil {
				return 0, e
			}
			txSvc := &TaskService{Queries: q}
			if e = txSvc.validateTaskExecutionScope(ctx, project.WorkspaceID, agent.ID, project.ID, squad); e != nil {
				return 0, e
			}
			if !CanMemberInvokeAgent(ctx, q, agent, row.ConfiguredBy, project.WorkspaceID) {
				return 0, ErrProjectSupervisionForbidden
			}
			if _, e = q.SetSupervisionIssueAssignment(ctx, db.SetSupervisionIssueAssignmentParams{ID: id, WorkspaceID: project.WorkspaceID, ProjectID: project.ID, Revision: action.Revision, AssigneeType: pgtype.Text{String: action.AssigneeType, Valid: true}, AssigneeID: assignee}); e != nil {
				return 0, e
			}
			changed++
		case "advance":
			if !config.AutoAdvance {
				return 0, errors.New("stage advancement is disabled by project policy")
			}
			allowed, e := s.stageReady(ctx, q, project, issue)
			if e != nil {
				return 0, e
			}
			if !allowed {
				return 0, errors.New("stage dependencies or cancelled work require a decision")
			}
			if _, e = q.PromoteSupervisionIssue(ctx, db.PromoteSupervisionIssueParams{ID: id, WorkspaceID: project.WorkspaceID, ProjectID: project.ID, Revision: action.Revision}); e != nil {
				return 0, e
			}
			changed++
		case "review":
			if issue.Status != "in_progress" {
				return 0, errors.New("only in-progress work can enter review")
			}
			evidence, err := q.GetSupervisionIssueEvidence(ctx, id)
			if err != nil {
				return 0, err
			}
			if evidence.ActiveRuns > 0 || !evidence.HasDelivery {
				return 0, errors.New("review requires completed delivery evidence and no active run")
			}
			if _, e = q.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: id, WorkspaceID: project.WorkspaceID, Status: "in_review", SourceTaskID: task.ID}); e != nil {
				return 0, e
			}
			changed++
		default:
			return 0, errors.New("unsupported coordination action")
		}
	}
	snapshot, err := s.snapshot(ctx, q, project, config, row.ConfiguredBy)
	if err != nil {
		return 0, err
	}
	queued, err := s.releaseReady(ctx, q, project, row, config, snapshot, task.ID)
	if err != nil {
		return 0, err
	}
	changed += len(queued)
	var contextData map[string]any
	if err = json.Unmarshal(task.Context, &contextData); err != nil {
		return 0, err
	}
	previous, _ := contextData["verified_actions"].(float64)
	contextData["verified_actions"] = int(previous) + changed
	contextData["requested_actions"] = progress.Requested + len(actions)
	var receipts []ProjectSupervisionAction
	if raw, err := json.Marshal(contextData["action_receipts"]); err != nil {
		return 0, err
	} else if string(raw) != "null" {
		if err = json.Unmarshal(raw, &receipts); err != nil {
			return 0, err
		}
	}
	contextData["action_receipts"] = append(receipts, actions...)
	raw, err := json.Marshal(contextData)
	if err != nil {
		return 0, err
	}
	if _, err = q.SetTaskProjectContext(ctx, db.SetTaskProjectContextParams{ID: task.ID, ContextPatch: raw}); err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	for _, run := range append(queued, queuedActions...) {
		s.Tasks.NotifyTaskEnqueued(ctx, run)
	}
	s.publishIssueChanges(ctx, snapshot, queued, beforeIssues)
	s.publish(project)
	return changed, nil
}

func (s *ProjectSupervisionService) authorizeRun(ctx context.Context, q *db.Queries, project db.Project, taskID pgtype.UUID, version int64) (db.ProjectSupervision, db.AgentTaskQueue, ProjectSupervisionConfig, error) {
	_, err := q.LockProjectForDelete(ctx, db.LockProjectForDeleteParams{ID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		return db.ProjectSupervision{}, db.AgentTaskQueue{}, ProjectSupervisionConfig{}, err
	}
	fresh, err := q.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		return db.ProjectSupervision{}, db.AgentTaskQueue{}, ProjectSupervisionConfig{}, err
	}
	project = fresh
	row, err := q.LockProjectSupervision(ctx, db.LockProjectSupervisionParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		return row, db.AgentTaskQueue{}, ProjectSupervisionConfig{}, err
	}
	task, err := q.GetAgentTask(ctx, taskID)
	if err != nil {
		return row, task, ProjectSupervisionConfig{}, err
	}
	contextData, ok := ProjectCoordination(task)
	if !row.Enabled || project.Status != "in_progress" || !ok || contextData.ProjectID != util.UUIDToString(project.ID) || contextData.WorkspaceID != util.UUIDToString(project.WorkspaceID) || contextData.PolicyRevision != row.Revision || contextData.CheckedVersion != version || task.AgentID != project.LeadID || project.LeadType.String != "agent" || !supervisionTaskActive(task.Status) {
		return row, task, ProjectSupervisionConfig{}, ErrProjectSupervisionForbidden
	}
	config, err := supervisionConfig(row.Config)
	member, e := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: row.ConfiguredBy, WorkspaceID: project.WorkspaceID})
	if e != nil || (member.Role != "owner" && member.Role != "admin") {
		return row, task, config, ErrProjectSupervisionForbidden
	}
	agent, e := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: task.AgentID, WorkspaceID: project.WorkspaceID})
	if e != nil || agent.ArchivedAt.Valid || !CanMemberInvokeAgent(ctx, q, agent, row.ConfiguredBy, project.WorkspaceID) {
		return row, task, config, ErrProjectSupervisionForbidden
	}
	return row, task, config, err
}

func (s *ProjectSupervisionService) Report(ctx context.Context, project db.Project, report ProjectSupervisionReport) error {
	if report.Summary == "" || len(report.Summary) > 8000 || len(report.WaitReason) > 2000 {
		return errors.New("coordination report needs a bounded summary")
	}
	if report.Decision != "action" && report.Decision != "wait" && report.Decision != "blocked" && report.Decision != "needs_human" {
		return errors.New("invalid coordination decision")
	}
	id, err := util.ParseUUID(report.TaskID)
	if err != nil {
		return err
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
	row, task, config, err := s.authorizeRun(ctx, q, project, id, report.CheckedVersion)
	if err != nil {
		return err
	}
	if report.Decision == "needs_human" {
		delivered, err := q.HasPendingHumanRequestForTask(ctx, db.HasPendingHumanRequestForTaskParams{TaskID: task.ID, WorkspaceID: project.WorkspaceID, ProjectID: project.ID, RecipientID: row.ConfiguredBy})
		if err != nil {
			return err
		}
		if !delivered {
			return errors.New("deliver the member's exact next action with multica human-request create before reporting needs_human")
		}
	}
	var prior ProjectSupervisionReport
	if err = json.Unmarshal(row.LastResult, &prior); err != nil {
		return err
	}
	if prior.TaskID == report.TaskID && row.HandledVersion >= report.CheckedVersion {
		if prior.Decision != report.Decision || prior.Summary != report.Summary || prior.WaitReason != report.WaitReason {
			return ErrProjectSupervisionConflict
		}
		return tx.Commit(ctx)
	}
	var contextData struct {
		Verified int                        `json:"verified_actions"`
		Actions  []ProjectSupervisionAction `json:"action_receipts"`
	}
	if err = json.Unmarshal(task.Context, &contextData); err != nil {
		return err
	}
	count := row.NoProgressCount
	if contextData.Verified > 0 {
		count = 0
	} else {
		snapshot, err := s.snapshot(ctx, q, project, config, row.ConfiguredBy)
		if err != nil {
			return err
		}
		if report.Decision != "wait" || snapshot.Actionable > 0 {
			count++
		}
	}
	reason := report.Decision
	if report.Decision == "action" && contextData.Verified == 0 {
		reason = "no_verified_progress"
	}
	if report.Decision == "needs_human" {
		count = int32(config.NoProgressLimit)
		reason = "needs_human"
	}
	if count >= int32(config.NoProgressLimit) {
		reason = "needs_human"
	}
	raw, err := json.Marshal(map[string]any{"decision": report.Decision, "summary": report.Summary, "wait_reason": report.WaitReason, "verified_actions": contextData.Verified, "actions": contextData.Actions, "task_id": report.TaskID, "checked_version": report.CheckedVersion})
	if err != nil {
		return err
	}
	if _, err = q.StoreProjectSupervisionResult(ctx, db.StoreProjectSupervisionResultParams{ProjectID: project.ID, WorkspaceID: project.WorkspaceID, HandledVersion: report.CheckedVersion, LastResult: raw, NoProgressCount: count, LastReason: reason}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.publish(project)
	return nil
}
