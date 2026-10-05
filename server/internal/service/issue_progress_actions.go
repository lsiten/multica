package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ProgressActionInput binds a member operation to the issue and run inspected in its card.
type ProgressActionInput struct {
	Kind          string `json:"kind"`
	IssueRevision int64  `json:"issue_revision"`
	RunID         string `json:"run_id"`
	Key           string `json:"key"`
	Text          string `json:"text,omitempty"`
	Verdict       string `json:"verdict,omitempty"`
}

// AcceptProgressReview closes only the exact delivered work the member inspected.
func (s *TaskService) AcceptProgressReview(ctx context.Context, issue db.Issue, memberID pgtype.UUID, input ProgressActionInput) (db.Issue, error) {
	var accepted db.Issue
	if err := validateProgressAction(input); err != nil {
		return accepted, err
	}
	err := s.runInTx(ctx, func(q *db.Queries) error {
		if _, err := q.LockVscreenWorkspace(ctx, issue.WorkspaceID); err != nil {
			return err
		}
		if _, err := q.LockActiveMember(ctx, db.LockActiveMemberParams{UserID: memberID, WorkspaceID: issue.WorkspaceID}); err != nil {
			return ErrHumanRequestForbidden
		}
		current, err := q.LockIssueForDescriptionUpdate(ctx, db.LockIssueForDescriptionUpdateParams{ID: issue.ID, WorkspaceID: issue.WorkspaceID})
		if err != nil {
			return err
		}
		latest, err := q.GetLatestProgressTask(ctx, current.ID)
		if err != nil {
			return err
		}
		if util.UUIDToString(latest.ID) != input.RunID || current.Revision != input.IssueRevision {
			return ErrProgressSnapshotChanged
		}
		scope, err := q.GetIssueNextStepScope(ctx, db.GetIssueNextStepScopeParams{IssueID: current.ID, WorkspaceID: current.WorkspaceID})
		if err != nil {
			return err
		}
		step := parseIssueNextStep(latest.Context, current.Revision, input.RunID, scope)
		if current.Status == "done" || current.Status == "cancelled" || current.Status != "in_review" && (step == nil || step.Kind != "review") {
			return ErrProgressSnapshotChanged
		}
		if step != nil && (step.ActorType == "agent" || step.ActorType == "member" && step.ActorID != util.UUIDToString(memberID)) {
			return ErrHumanRequestForbidden
		}
		evidence, err := q.GetSupervisionIssueEvidence(ctx, current.ID)
		if err != nil {
			return err
		}
		pending, err := q.HasPendingProgressHumanRequest(ctx, db.HasPendingProgressHumanRequestParams{WorkspaceID: current.WorkspaceID, IssueID: current.ID})
		if err != nil {
			return err
		}
		if pending || evidence.ActiveRuns > 0 || !evidence.HasDelivery || progressDeliverySummary(latest.Result) == "" || evidence.DependencyBlocked {
			return ErrProgressSnapshotChanged
		}
		goal, goalErr := q.GetIssueGoal(ctx, current.ID)
		if goalErr != nil && !errors.Is(goalErr, pgx.ErrNoRows) {
			return goalErr
		}
		if goalErr == nil && !goal.CompletedAt.Valid {
			return fmt.Errorf("%w: complete the issue objective before acceptance", ErrProgressSnapshotChanged)
		}
		accepted, err = q.UpdateIssue(ctx, db.UpdateIssueParams{ID: current.ID, ExpectedRevision: pgtype.Int8{Int64: current.Revision, Valid: true}, Status: pgtype.Text{String: "done", Valid: true}, AssigneeType: current.AssigneeType, AssigneeID: current.AssigneeID, StartDate: current.StartDate, DueDate: current.DueDate, ParentIssueID: current.ParentIssueID, ProjectID: current.ProjectID, Stage: current.Stage})
		return err
	})
	return accepted, err
}

func validateProgressAction(input ProgressActionInput) error {
	if input.IssueRevision < 1 || !shortText(input.Key, 100) || len(input.Text) > 8000 {
		return ErrHumanRequestInput
	}
	return nil
}

func progressActionFingerprint(input ProgressActionInput) string {
	raw, _ := json.Marshal(input)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func progressActionCandidates(row db.ListProgressIssuesRow, entry *ProgressEntry, memberID string) {
	entry.Revision = row.Revision
	entry.Actions = []ProgressAction{}
	entry.NextStep = parseIssueNextStep(row.LatestRunContext, row.Revision, util.UUIDToString(row.LatestRunID), row.NextStepScope)
	if row.LatestRunStatus == "failed" || row.LatestRunStatus == "cancelled" {
		entry.NextStep = nil
	}
	actorType, actorID := row.AssigneeType.String, util.UUIDToPtr(row.AssigneeID)
	if entry.NextStep != nil {
		actorType = entry.NextStep.ActorType
		if entry.NextStep.ActorID != "" {
			id := entry.NextStep.ActorID
			actorID = &id
		} else {
			actorID = nil
		}
		if actorType == "member" && actorID != nil && *actorID == memberID {
			entry.NeedsMe = true
		}
	}
	add := func(kind string) {
		entry.Actions = append(entry.Actions, ProgressAction{Kind: kind, ActorType: actorType, ActorID: actorID, NeedsMe: actorType == "member" && actorID != nil && *actorID == memberID, Enabled: false})
		if (kind == "continue" || kind == "inspect_continue" || kind == "provide_info" || kind == "rerun") && (row.AssigneeArchived || row.AssigneeMissing || row.RuntimeMissing || row.RuntimeStatus == "offline") {
			entry.Actions = append(entry.Actions, ProgressAction{Kind: "resolve_runtime", ActorType: row.AssigneeType.String, ActorID: util.UUIDToPtr(row.AssigneeID), Enabled: true})
		}
	}
	for _, request := range entry.Requests {
		kind := "respond"
		if entry.NextStep != nil && entry.NextStep.Kind == "manual_action" {
			kind = "manual"
		}
		id := request.RecipientID
		entry.Actions = append(entry.Actions, ProgressAction{Kind: kind, ActorType: "member", ActorID: &id, NeedsMe: request.NeedsMe, Enabled: request.NeedsMe, RequestID: request.ID, DisabledReason: func() string {
			if request.NeedsMe {
				return ""
			}
			return "other_member"
		}()})
	}
	if len(entry.Actions) > 0 {
		return
	}
	if len(entry.DirectBlockers) > 0 {
		targets := entry.RootBlockers
		if len(targets) == 0 {
			targets = entry.DirectBlockers
		}
		for _, blocker := range targets {
			entry.Actions = append(entry.Actions, ProgressAction{Kind: "open_blocker", ActorType: "unknown", Enabled: true, TargetIssueID: blocker.ID})
		}
		return
	}
	if row.ActiveRunID.Valid || !progressOpen(row) {
		return
	}
	if entry.NextStep != nil {
		switch entry.NextStep.Kind {
		case "needs_information":
			add("provide_info")
			return
		case "review":
			add("review")
			return
		case "continue":
			add("continue")
			return
		case "blocked":
			add("inspect_continue")
			return
		}
	}
	if row.Status == "in_review" {
		actorType, actorID = "unknown", nil
		add("review")
		return
	}
	if row.AssigneeArchived || row.AssigneeMissing || row.RuntimeMissing || row.RuntimeStatus == "offline" {
		add("resolve_runtime")
		return
	}
	if !row.AssigneeID.Valid {
		add("assign")
		return
	}
	if row.AssigneeType.String == "member" && row.Status != "backlog" && row.Status != "triage" {
		add("member_work")
		if actorID != nil && *actorID == memberID {
			entry.NeedsMe = true
		}
		return
	}
	if row.LatestRunStatus == "failed" {
		add("rerun")
		return
	}
	if row.LatestRunStatus == "completed" {
		add("inspect_continue")
		return
	}

	if row.Status != "backlog" && row.Status != "triage" {
		add("continue")
	}
}

// PerformProgressAction creates at most one continuation without replaying successful work as a fresh session.
func (s *TaskService) PerformProgressAction(ctx context.Context, issue db.Issue, memberID pgtype.UUID, input ProgressActionInput) (db.AgentTaskQueue, error) {
	var result db.AgentTaskQueue
	if err := validateProgressAction(input); err != nil {
		return result, err
	}
	switch input.Kind {
	case "continue", "inspect_continue", "provide_info", "review", "rerun":
	default:
		return result, ErrHumanRequestInput
	}
	if input.Kind == "provide_info" && strings.TrimSpace(input.Text) == "" {
		return result, ErrHumanRequestInput
	}
	agentID := issue.AssigneeID
	squadID := pgtype.UUID{}
	sourceHandoff := false
	isLeader := false
	if issue.AssigneeType.String == "member" && (input.Kind == "provide_info" || input.Kind == "review" && input.Verdict == "changes") {
		source, err := s.Queries.GetLatestProgressTask(ctx, issue.ID)
		if err != nil {
			return result, err
		}
		scope, err := s.Queries.GetIssueNextStepScope(ctx, db.GetIssueNextStepScopeParams{IssueID: issue.ID, WorkspaceID: issue.WorkspaceID})
		if err != nil {
			return result, err
		}
		step := parseIssueNextStep(source.Context, issue.Revision, util.UUIDToString(source.ID), scope)
		if step != nil && (input.Kind == "provide_info" && step.Kind == "needs_information" || input.Kind == "review" && step.Kind == "review") {
			agentID, squadID, isLeader, sourceHandoff = source.AgentID, source.SquadID, source.IsLeaderTask, true
		}
	}
	if issue.AssigneeType.String == "squad" {
		squad, err := s.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{ID: issue.AssigneeID, WorkspaceID: issue.WorkspaceID})
		if err != nil {
			return result, err
		}
		agentID, squadID, isLeader = squad.LeaderID, squad.ID, true
	}
	if issue.AssigneeType.String != "agent" && issue.AssigneeType.String != "squad" && !sourceHandoff {
		return result, ErrHumanRequestForbidden
	}
	prepared, err := s.PrepareChatTaskEnqueue(ctx, agentID, memberID)
	if err != nil {
		return result, err
	}
	replayed := false
	var reply db.Comment
	err = s.runInTx(ctx, func(q *db.Queries) error {
		if _, err := q.LockVscreenWorkspace(ctx, issue.WorkspaceID); err != nil {
			return err
		}
		if _, err := q.LockActiveMember(ctx, db.LockActiveMemberParams{UserID: memberID, WorkspaceID: issue.WorkspaceID}); err != nil {
			return ErrHumanRequestForbidden
		}
		agent, err := q.GetAgentForClaimUpdate(ctx, agentID)
		if err != nil {
			return err
		}
		if agent.WorkspaceID != issue.WorkspaceID || agent.ArchivedAt.Valid || !agent.RuntimeID.Valid || !CanMemberInvokeAgent(ctx, q, agent, memberID, issue.WorkspaceID) {
			return ErrHumanRequestForbidden
		}
		current, err := q.LockIssueForDescriptionUpdate(ctx, db.LockIssueForDescriptionUpdateParams{ID: issue.ID, WorkspaceID: issue.WorkspaceID})
		if err != nil {
			return err
		}
		prior, priorErr := q.FindProgressActionTask(ctx, db.FindProgressActionTaskParams{IssueID: current.ID, MemberID: memberID, ActionKey: input.Key})
		if priorErr == nil {
			var recorded struct {
				Fingerprint string `json:"progress_action_fingerprint"`
			}
			if json.Unmarshal(prior.Context, &recorded) != nil || recorded.Fingerprint != progressActionFingerprint(input) {
				return ErrProgressSnapshotChanged
			}
			result = prior
			replayed = true
			return nil
		}
		if !errors.Is(priorErr, pgx.ErrNoRows) {
			return priorErr
		}
		if current.Revision != input.IssueRevision || current.AssigneeID != issue.AssigneeID || current.AssigneeType != issue.AssigneeType {
			return ErrProgressSnapshotChanged
		}
		runtime, err := q.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{ID: agent.RuntimeID, WorkspaceID: current.WorkspaceID})
		if err != nil || runtime.Status == "offline" {
			return ErrProgressSnapshotChanged
		}
		if squadID.Valid {
			squad, err := q.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{ID: squadID, WorkspaceID: current.WorkspaceID})
			if err != nil || squad.ArchivedAt.Valid || isLeader && squad.LeaderID != agent.ID {
				return ErrProgressSnapshotChanged
			}
		}
		pending, err := q.HasPendingProgressHumanRequest(ctx, db.HasPendingProgressHumanRequestParams{WorkspaceID: current.WorkspaceID, IssueID: current.ID})
		if err != nil {
			return err
		}
		if pending {
			return ErrProgressSnapshotChanged
		}
		catalog, catalogErr := q.GetIssueStatusEntryByKey(ctx, db.GetIssueStatusEntryByKeyParams{WorkspaceID: current.WorkspaceID, Key: current.Status})
		if catalogErr != nil && !errors.Is(catalogErr, pgx.ErrNoRows) {
			return catalogErr
		}
		if catalog.Category == "done" || catalog.Category == "closed" {
			return ErrProgressSnapshotChanged
		}
		latest, err := q.GetLatestProgressTask(ctx, current.ID)
		if errors.Is(err, pgx.ErrNoRows) && input.RunID == "" {
			latest = db.AgentTaskQueue{}
		} else if err != nil {
			return err
		}
		if util.UUIDToString(latest.ID) != input.RunID {
			return ErrProgressSnapshotChanged
		}
		evidence, err := q.GetSupervisionIssueEvidence(ctx, current.ID)
		if err != nil {
			return err
		}
		if evidence.ActiveRuns > 0 {
			return ErrProgressSnapshotChanged
		}
		if evidence.DependencyBlocked {
			return ErrProgressSnapshotChanged
		}
		if input.Kind == "rerun" && latest.Status != "failed" {
			return ErrProgressSnapshotChanged
		}
		if current.Status == "done" || current.Status == "cancelled" || current.Status == "backlog" || current.Status == "triage" {
			return ErrProgressSnapshotChanged
		}
		scope, err := q.GetIssueNextStepScope(ctx, db.GetIssueNextStepScopeParams{IssueID: current.ID, WorkspaceID: current.WorkspaceID})
		if err != nil {
			return err
		}
		step := parseIssueNextStep(latest.Context, current.Revision, util.UUIDToString(latest.ID), scope)
		if step != nil && (step.ActorType == "member" && step.ActorID != util.UUIDToString(memberID) || step.ActorType == "agent" && step.ActorID != util.UUIDToString(agent.ID)) {
			return ErrHumanRequestForbidden
		}
		if latest.Status == "failed" && input.Kind != "rerun" {
			return ErrProgressSnapshotChanged
		}
		if step != nil && (step.Kind == "needs_information" && input.Kind != "provide_info" || step.Kind == "review" && input.Kind != "review") {
			return ErrProgressSnapshotChanged
		}
		if current.Status == "in_review" && input.Kind != "review" {
			return ErrProgressSnapshotChanged
		}
		if input.Kind == "provide_info" && (step == nil || step.Kind != "needs_information") {
			return ErrProgressSnapshotChanged
		}
		if input.Kind == "review" {
			if current.Status == "done" || current.Status == "cancelled" || current.Status != "in_review" && (step == nil || step.Kind != "review") {
				return ErrProgressSnapshotChanged
			}
			if !evidence.HasDelivery || progressDeliverySummary(latest.Result) == "" {
				return ErrProgressSnapshotChanged
			}
			if input.Verdict != "changes" && input.Verdict != "accept" {
				return ErrHumanRequestInput
			}
			if input.Verdict == "accept" {
				return fmt.Errorf("acceptance must use the guarded issue status action")
			}
			if strings.TrimSpace(input.Text) == "" {
				return ErrHumanRequestInput
			}
		}
		scoped := &TaskService{Queries: q}
		if err := scoped.validateTaskExecutionScope(ctx, current.WorkspaceID, agent.ID, current.ProjectID, squadID); err != nil {
			return err
		}
		text := input.Text
		if text == "" {
			text = "Inspect the latest delivery, handoff and recorded prerequisites for this issue. Name the remaining work and who must act. Continue only within the current issue's scope, preserving already verified results; do not blindly rerun completed work. Report the next step and any required human request before ending."
		}
		comment, err := q.CreateComment(ctx, db.CreateCommentParams{ID: dbid.NewV7(), WorkspaceID: current.WorkspaceID, IssueID: current.ID, AuthorType: "member", AuthorID: memberID, Content: text, Type: "comment"})
		if err != nil {
			return err
		}
		reply = comment.Comment()
		rerunOf := pgtype.UUID{}
		if input.Kind == "rerun" {
			rerunOf = latest.ID
		}
		result, err = q.CreateAgentTask(ctx, db.CreateAgentTaskParams{ID: dbid.NewV7(), AgentID: agent.ID, RuntimeID: agent.RuntimeID, IssueID: current.ID, Priority: 2, TriggerCommentID: comment.ID, OriginatorUserID: memberID, AccountableUserID: memberID, OriginatorSource: prepared.attrSource, TriggerEvidenceKind: pgtype.Text{String: "progress_action", Valid: true}, SquadID: squadID, IsLeaderTask: pgtype.Bool{Bool: isLeader, Valid: true}, ForceFreshSession: pgtype.Bool{Bool: input.Kind == "rerun", Valid: true}, RerunOfTaskID: rerunOf, RuntimeMcpOverlay: prepared.runtimeOverlay.Overlay, RuntimeConnectedApps: prepared.runtimeOverlay.ConnectedApps})
		if err != nil {
			return err
		}
		patch, err := json.Marshal(map[string]any{"progress_action_key": input.Key, "progress_action_kind": input.Kind, "progress_action_fingerprint": progressActionFingerprint(input), "progress_source_task_id": input.RunID})
		if err != nil {
			return err
		}
		result, err = q.SetTaskProjectContext(ctx, db.SetTaskProjectContextParams{ID: result.ID, ContextPatch: patch})
		return err
	})
	if err == nil && !replayed {
		if s.Bus != nil {
			s.Bus.Publish(events.Event{Type: protocol.EventCommentCreated, WorkspaceID: util.UUIDToString(issue.WorkspaceID), ActorType: "member", ActorID: util.UUIDToString(memberID), Payload: map[string]any{"comment": reply}})
		}
		s.broadcastTaskEvent(ctx, "task:queued", result)
		s.NotifyTaskEnqueued(ctx, result)
	}
	return result, err
}
