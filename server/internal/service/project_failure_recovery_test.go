package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestProjectTaskFailureReturnsToLeadWithoutSupervisorOrParentIssue(t *testing.T) {
	for _, source := range []string{"no_delegation", "project_supervision", "paused_project", "member_lead"} {
		t.Run(source, func(t *testing.T) {
			f, svc := seedDelegatedFailureFixture(t)
			fx := testutil.New(f.pool, f.workspaceID, f.userID)
			project := fx.Project(t, "Project recovery", testutil.Cols{"status": "in_progress", "lead_type": "agent", "lead_id": f.coordinator})
			fx.Exec(t, "UPDATE issue SET project_id=$2,status='in_progress' WHERE id=$1", f.workerIssue, project)
			failedID := fx.Task(t, f.worker, testutil.Cols{"runtime_id": f.runtimeID, "issue_id": f.workerIssue, "status": "running", "originator_user_id": f.userID, "accountable_user_id": f.userID, "max_attempts": 1})
			if source == "project_supervision" {
				coord, err := json.Marshal(ProjectCoordinationContext{Type: ProjectSupervisionContextType, ProjectID: project, WorkspaceID: f.workspaceID})
				if err != nil {
					t.Fatal(err)
				}
				fx.Exec(t, "UPDATE agent_task_queue SET issue_id=NULL,trigger_comment_id=NULL,context=$2 WHERE id=$1", f.sourceTask, coord)
				fx.Exec(t, "UPDATE agent_task_queue SET delegated_from_task_id=$2 WHERE id=$1", failedID, f.sourceTask)
			} else if source == "paused_project" {
				fx.Exec(t, "UPDATE project SET status='paused' WHERE id=$1", project)
			} else if source == "member_lead" {
				fx.Exec(t, "UPDATE project SET lead_type='member',lead_id=$2 WHERE id=$1", project, f.userID)
			}
			fx.Cleanup(t, "DELETE FROM comment WHERE source_task_id=$1", failedID)
			fx.Cleanup(t, "DELETE FROM agent_task_queue WHERE trigger_evidence_ref_id=$1", failedID)
			ctx := context.Background()
			if _, err := svc.FailTask(ctx, util.MustParseUUID(failedID), "worktree preparation failed", "", "", "", "agent_error.process_failure", false, "", ""); err != nil {
				t.Fatal(err)
			}
			var tasks int
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM agent_task_queue WHERE trigger_evidence_kind='delegated_failure' AND trigger_evidence_ref_id=$1", failedID).Scan(&tasks); err != nil {
				t.Fatal(err)
			}
			if source == "paused_project" || source == "member_lead" {
				if tasks != 0 {
					t.Fatalf("%s enqueued %d unauthorized coordination runs", source, tasks)
				}
				return
			}
			if tasks != 1 {
				t.Fatalf("%s failure did not wake the project lead: got %d recovery tasks", source, tasks)
			}
			var agent, issue, content string
			if err := f.pool.QueryRow(ctx, `SELECT t.agent_id::text,t.issue_id::text,c.content FROM agent_task_queue t JOIN comment c ON c.id=t.trigger_comment_id WHERE t.trigger_evidence_ref_id=$1`, failedID).Scan(&agent, &issue, &content); err != nil {
				t.Fatal(err)
			}
			if agent != f.coordinator || issue != f.workerIssue || !strings.Contains(content, "worktree preparation failed") || !strings.Contains(content, "diagnose") || !strings.Contains(content, "continue") {
				t.Fatalf("project recovery lost its target or repair context: agent=%s issue=%s content=%s", agent, issue, content)
			}
			failed, err := svc.Queries.GetAgentTask(ctx, util.MustParseUUID(failedID))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.recoverDelegatedTaskFailure(ctx, failed); err != nil {
				t.Fatal(err)
			}
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM agent_task_queue WHERE trigger_evidence_ref_id=$1", failedID).Scan(&tasks); err != nil || tasks != 1 {
				t.Fatalf("project failure was replayed twice: count=%d error=%v", tasks, err)
			}
		})
	}
}

func TestAutopilotCoordinatorReceivesDelegatedFailureThroughDurableOutbox(t *testing.T) {
	f, svc := seedDelegatedFailureFixture(t)
	fx := testutil.New(f.pool, f.workspaceID, f.userID)
	ap := fx.Insert(t, "autopilot", testutil.Cols{"workspace_id": f.workspaceID, "title": "Project patrol", "assignee_id": f.coordinator, "created_by_type": "member", "created_by_id": f.userID})
	run := fx.Insert(t, "autopilot_run", testutil.Cols{"autopilot_id": ap, "source": "manual", "status": "completed"})
	fx.Exec(t, "UPDATE agent_task_queue SET autopilot_run_id=$2 WHERE id=$1", f.sourceTask, run)
	failed := fx.Task(t, f.worker, testutil.Cols{"runtime_id": f.runtimeID, "issue_id": f.workerIssue, "status": "failed", "delegated_from_task_id": f.sourceTask, "failure_reason": "agent_error.process_failure", "originator_user_id": f.userID, "accountable_user_id": f.userID})
	fx.Cleanup(t, "DELETE FROM comment WHERE source_task_id=$1", failed)
	fx.Cleanup(t, "DELETE FROM agent_task_queue WHERE trigger_evidence_ref_id=$1", failed)
	ctx := context.Background()
	target, created, err := svc.ensureDelegatedFailureRecoveryComment(ctx, util.MustParseUUID(failed))
	if err != nil || target == nil || !created {
		t.Fatalf("autopilot source lost its worker failure: created=%v error=%v", created, err)
	}
	pending, err := svc.Queries.ListPendingDelegatedFailureRecoveries(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, comment := range pending {
		found = found || comment.ID == target.comment.ID
	}
	if !found {
		t.Fatal("autopilot failure signal cannot survive a dispatch interruption")
	}
	if _, err = svc.RecoverPendingDelegatedFailures(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var task db.AgentTaskQueue
	if err := f.pool.QueryRow(ctx, "SELECT id FROM agent_task_queue WHERE trigger_evidence_ref_id=$1", failed).Scan(&task.ID); err != nil {
		t.Fatal(err)
	}
	queued, err := svc.Queries.GetAgentTask(ctx, task.ID)
	if err != nil || queued.AgentID != util.MustParseUUID(f.coordinator) || queued.AutopilotRunID.Valid {
		t.Fatalf("recovery must resume coordination without rerunning the autopilot: %+v %v", queued, err)
	}
}

func TestProjectFailureRecoverySignalSurvivesDispatchInterruptionAndSettlesCancellation(t *testing.T) {
	f, svc := seedDelegatedFailureFixture(t)
	fx := testutil.New(f.pool, f.workspaceID, f.userID)
	project := fx.Project(t, "Recover interrupted dispatch", testutil.Cols{"status": "in_progress", "lead_type": "agent", "lead_id": f.coordinator})
	fx.Exec(t, "UPDATE issue SET project_id=$2,status='in_progress' WHERE id=$1", f.workerIssue, project)
	failed := fx.Task(t, f.worker, testutil.Cols{"runtime_id": f.runtimeID, "issue_id": f.workerIssue, "status": "failed", "failure_reason": "agent_error.process_failure", "originator_user_id": f.userID, "accountable_user_id": f.userID})
	fx.Cleanup(t, "DELETE FROM comment WHERE source_task_id=$1", failed)
	fx.Cleanup(t, "DELETE FROM agent_task_queue WHERE trigger_evidence_ref_id=$1", failed)
	ctx := context.Background()
	target, created, err := svc.ensureDelegatedFailureRecoveryComment(ctx, parseTestUUID(t, failed))
	if err != nil || target == nil || !created {
		t.Fatalf("project recovery signal was not persisted: created=%v error=%v", created, err)
	}
	pending, err := svc.Queries.ListPendingDelegatedFailureRecoveries(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, comment := range pending {
		found = found || comment.ID == target.comment.ID
	}
	if !found {
		t.Fatal("project lead recovery was lost before dispatch")
	}
	if _, err := svc.RecoverPendingDelegatedFailures(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err := f.pool.QueryRow(ctx, "SELECT id FROM agent_task_queue WHERE trigger_evidence_ref_id=$1", failed).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CancelTaskByUser(ctx, parseTestUUID(t, taskID), TaskCancellationActor{Type: "member", ID: parseTestUUID(t, f.userID), Name: "Project operator"}); err != nil {
		t.Fatal(err)
	}
	comment, err := svc.Queries.GetComment(ctx, target.comment.ID)
	if err != nil || !comment.RecoverySettledAt.Valid {
		t.Fatalf("explicit recovery cancellation did not settle its receipt: %+v %v", comment, err)
	}
}
