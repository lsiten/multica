package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func supervisionFixture(t *testing.T) (principalFixture, *ProjectSupervisionService, db.Project, string) {
	t.Helper()
	f, owner := newPrincipalFixture(t)
	ctx := context.Background()
	if err := f.q.SeedIssueStatusEntries(ctx, parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	f.Cleanup(t, "DELETE FROM issue_status WHERE workspace_id=$1", f.WorkspaceID)
	runtime := f.Runtime(t, "supervision", testutil.Cols{"metadata": testutil.Raw(`'{"capabilities":["project-supervision-v1"]}'::jsonb`)})
	lead := f.Agent(t, "project lead", runtime)
	projectID := f.Project(t, "supervised", testutil.Cols{"status": "in_progress", "lead_type": "agent", "lead_id": lead})
	project, err := f.q.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: parseTestUUID(t, projectID), WorkspaceID: parseTestUUID(t, f.WorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	f.Cleanup(t, "DELETE FROM project_supervision WHERE project_id=$1", project.ID)
	f.Cleanup(t, "DELETE FROM agent_task_queue WHERE context->>'project_id'=$1", projectID)
	s := &ProjectSupervisionService{Tasks: f.svc.TaskSvc}
	if err = s.Save(ctx, project, parseTestUUID(t, owner), true, DefaultProjectSupervisionConfig(), 0); err != nil {
		t.Fatal(err)
	}
	return f, s, project, lead
}
func supervisionCheck(t *testing.T, s *ProjectSupervisionService, p db.Project) ProjectSupervisionView {
	t.Helper()
	if err := s.CheckNow(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	view, err := s.View(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestProjectSupervisionCoalescesEventsAndReplaysNewFacts(t *testing.T) {
	f, s, p, _ := supervisionFixture(t)
	s.Listen()
	ctx := context.Background()
	issue := f.Issue(t, "unassigned", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "todo"})
	v := supervisionCheck(t, s, p)
	if v.LastTaskID == nil {
		t.Fatalf("no coordination: %+v", v)
	}
	taskID := parseTestUUID(t, *v.LastTaskID)
	for range 100 {
		s.Tasks.Bus.Publish(events.Event{Type: "issue:updated", WorkspaceID: f.WorkspaceID, Payload: map[string]any{"issue": map[string]string{"id": issue, "project_id": util.UUIDToString(p.ID)}}})
	}
	v = supervisionCheck(t, s, p)
	task, err := f.q.GetAgentTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	capture, ok := ProjectCoordination(task)
	if !ok || capture.CheckedVersion != v.DirtyVersion {
		t.Fatalf("queued facts not merged: %+v %+v", capture, v)
	}
	f.Exec(t, "UPDATE agent_task_queue SET status='running',started_at=now() WHERE id=$1", taskID)
	late := f.Issue(t, "late assignment", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "todo"})
	v = supervisionCheck(t, s, p)
	frozen, err := f.q.GetAgentTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	frozenCtx, _ := ProjectCoordination(frozen)
	if frozenCtx.CheckedVersion != capture.CheckedVersion {
		t.Fatal("running context mutated")
	}
	if n := f.Count(t, "SELECT count(*) FROM agent_task_queue WHERE context->>'project_id'=$1", util.UUIDToString(p.ID)); n != 1 {
		t.Fatalf("%d coordinator runs", n)
	}
	err = s.Report(ctx, p, ProjectSupervisionReport{TaskID: *v.LastTaskID, CheckedVersion: capture.CheckedVersion, Decision: "wait", Summary: "Waiting for assignment"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.View(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if view.HandledVersion >= view.DirtyVersion {
		t.Fatal("late dirty version lost")
	}
	f.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", taskID)
	v = supervisionCheck(t, s, p)
	if v.LastTaskID == nil || *v.LastTaskID == util.UUIDToString(taskID) {
		t.Fatalf("new facts not replayed: %s %+v", late, v)
	}
}

func TestProjectSupervisionReleaseIsBoundedAndClaimCapIsAtomic(t *testing.T) {
	f, s, p, _ := supervisionFixture(t)
	ctx := context.Background()
	rt := f.Runtime(t, "workers")
	worker := f.Agent(t, "worker", rt, testutil.Cols{"max_concurrent_tasks": 100})
	for n := range 100 {
		issue := f.Issue(t, fmt.Sprintf("ready-%d", n), testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "todo", "assignee_type": "agent", "assignee_id": worker})
		f.Cleanup(t, "DELETE FROM agent_task_queue WHERE issue_id=$1", issue)
	}
	v := supervisionCheck(t, s, p)
	if v.LastReason != "work_released" || v.Snapshot.Counts.Executing != 3 {
		t.Fatalf("release exceeded batch: %+v", v)
	}
	f.Exec(t, "UPDATE agent_task_queue SET status='running',started_at=now() WHERE agent_id=$1", worker)
	first := f.Issue(t, "extra", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "todo"})
	queued := f.Task(t, worker, testutil.Cols{"issue_id": first, "runtime_id": rt, "originator_user_id": f.UserID, "accountable_user_id": f.UserID})
	claimed, err := s.Tasks.ClaimTask(ctx, parseTestUUID(t, worker))
	if err != nil || claimed != nil {
		t.Fatalf("project full should wait: %+v %v", claimed, err)
	}
	f.Exec(t, "UPDATE agent_task_queue SET status='completed' WHERE agent_id=$1 AND status='running'", worker)
	// Concurrent claims on different agents serialize against the project fence.
	row, err := f.q.GetProjectSupervision(ctx, db.GetProjectSupervisionParams{ProjectID: p.ID, WorkspaceID: p.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultProjectSupervisionConfig()
	cfg.MaxInFlight = 1
	cfg.BatchSize = 1
	if err = s.Save(ctx, p, parseTestUUID(t, f.UserID), true, cfg, row.Revision); err != nil {
		t.Fatal(err)
	}
	secondAgent := f.Agent(t, "second worker", rt)
	secondIssue := f.Issue(t, "second", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "todo"})
	f.Task(t, secondAgent, testutil.Cols{"issue_id": secondIssue, "runtime_id": rt, "originator_user_id": f.UserID, "accountable_user_id": f.UserID})
	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	var errs []error
	for _, id := range []string{worker, secondAgent} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			task, e := s.Tasks.ClaimTask(ctx, parseTestUUID(t, id))
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				errs = append(errs, e)
			}
			if task != nil {
				count++
			}
		}(id)
	}
	wg.Wait()
	if len(errs) > 0 || count != 1 {
		t.Fatalf("atomic admission: count=%d errors=%v queued=%s", count, errs, queued)
	}
}

func TestProjectSupervisionScopeActionsReportAndLeadChange(t *testing.T) {
	f, s, p, _ := supervisionFixture(t)
	ctx := context.Background()
	issue := f.Issue(t, "ready", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "todo"})
	rt := f.Runtime(t, "worker")
	worker := f.Agent(t, "worker", rt)
	v := supervisionCheck(t, s, p)
	id := parseTestUUID(t, *v.LastTaskID)
	task, err := f.q.GetAgentTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	coord, _ := ProjectCoordination(task)
	f.Exec(t, "UPDATE agent_task_queue SET status='running',started_at=now() WHERE id=$1", id)
	foreign := f.Project(t, "other")
	foreignIssue := f.Issue(t, "foreign", testutil.Cols{"project_id": foreign, "status": "todo"})
	foreignRow, err := f.q.GetIssue(ctx, parseTestUUID(t, foreignIssue))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, p, id, coord.CheckedVersion, []ProjectSupervisionAction{{Kind: "assign", IssueID: foreignIssue, Revision: foreignRow.Revision, AssigneeType: "agent", AssigneeID: worker}}); !errors.Is(err, ErrProjectSupervisionConflict) {
		t.Fatalf("foreign action: %v", err)
	}
	row, err := f.q.GetIssue(ctx, parseTestUUID(t, issue))
	if err != nil {
		t.Fatal(err)
	}
	updates := 0
	s.Tasks.Bus.Subscribe("issue:updated", func(event events.Event) { updates++ })
	f.Cleanup(t, "DELETE FROM agent_task_queue WHERE issue_id=$1", issue)
	n, err := s.Apply(ctx, p, id, coord.CheckedVersion, []ProjectSupervisionAction{{Kind: "assign", IssueID: issue, Revision: row.Revision, AssigneeType: "agent", AssigneeID: worker}})
	if err != nil || n < 1 {
		t.Fatalf("assign: n=%d err=%v", n, err)
	}
	if updates != 1 {
		t.Fatalf("expected one authoritative issue update, got %d", updates)
	}
	if n := f.Count(t, "SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND delegated_from_task_id=$2", issue, id); n != 1 {
		t.Fatalf("missing coordinator delegation lineage: %d", n)
	}
	report := ProjectSupervisionReport{TaskID: *v.LastTaskID, CheckedVersion: coord.CheckedVersion, Decision: "action", Summary: "Assigned worker"}
	if err = s.Report(ctx, p, report); err != nil {
		t.Fatal(err)
	}
	if err = s.Report(ctx, p, report); err != nil {
		t.Fatal(err)
	}
	view, err := s.View(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if view.NoProgressCount != 0 {
		t.Fatal("verified progress not recorded")
	}
	f.Exec(t, "UPDATE project SET status='paused' WHERE id=$1", p.ID)
	if _, err = s.Apply(ctx, p, id, coord.CheckedVersion, []ProjectSupervisionAction{{Kind: "review", IssueID: issue, Revision: 1}}); !errors.Is(err, ErrProjectSupervisionForbidden) {
		t.Fatalf("paused project accepted actions: %v", err)
	}
	f.Exec(t, "UPDATE project SET status='in_progress' WHERE id=$1", p.ID)
	f.Exec(t, "UPDATE project SET lead_id=$2 WHERE id=$1", p.ID, worker)
	if _, err = s.Apply(ctx, p, id, coord.CheckedVersion, []ProjectSupervisionAction{{Kind: "review", IssueID: issue, Revision: 1}}); !errors.Is(err, ErrProjectSupervisionForbidden) {
		t.Fatalf("stale lead authority: %v", err)
	}
}

func TestProjectSupervisionRejectsFalseProgressAndInvalidPolicy(t *testing.T) {
	f, s, p, _ := supervisionFixture(t)
	ctx := context.Background()
	f.Issue(t, "ready", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "todo"})
	v := supervisionCheck(t, s, p)
	id := parseTestUUID(t, *v.LastTaskID)
	task, _ := f.q.GetAgentTask(ctx, id)
	coord, _ := ProjectCoordination(task)
	f.Exec(t, "UPDATE agent_task_queue SET status='running',started_at=now() WHERE id=$1", id)
	if err := s.Report(ctx, p, ProjectSupervisionReport{TaskID: *v.LastTaskID, CheckedVersion: coord.CheckedVersion, Decision: "action", Summary: "I inspected it"}); err != nil {
		t.Fatal(err)
	}
	view, err := s.View(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if view.NoProgressCount != 1 || view.LastReason != "no_verified_progress" {
		t.Fatalf("false progress counted: %+v", view)
	}
	config := DefaultProjectSupervisionConfig()
	config.ReadyStatuses = []string{"in_progress"}
	if err = s.Save(ctx, p, parseTestUUID(t, f.UserID), true, config, view.Revision); err == nil {
		t.Fatal("started status admitted as ready")
	}
	var result map[string]any
	if err = json.Unmarshal(view.LastResult, &result); err != nil {
		t.Fatal(err)
	}
	if result["verified_actions"] != float64(0) {
		t.Fatal(result)
	}
}

func TestProjectSupervisionMissingReportsStopAfterLimit(t *testing.T) {
	f, s, p, _ := supervisionFixture(t)
	ctx := context.Background()
	f.Issue(t, "unassigned", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "todo"})
	var ids []string
	for range 3 {
		policy, err := f.q.GetProjectSupervision(ctx, db.GetProjectSupervisionParams{ProjectID: p.ID, WorkspaceID: p.WorkspaceID})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.check(ctx, policy); err != nil {
			t.Fatal(err)
		}
		view, err := s.View(ctx, p)
		if err != nil || view.LastTaskID == nil {
			t.Fatalf("missing run: %v %+v", err, view)
		}
		ids = append(ids, *view.LastTaskID)
		f.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", *view.LastTaskID)
	}
	policy, err := f.q.GetProjectSupervision(ctx, db.GetProjectSupervisionParams{ProjectID: p.ID, WorkspaceID: p.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.check(ctx, policy); err != nil {
		t.Fatal(err)
	}
	view, err := s.View(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if view.NoProgressCount != 3 || view.LastReason != "needs_human" {
		t.Fatalf("unreported runs did not stop: ids=%v count=%d reason=%s", ids, view.NoProgressCount, view.LastReason)
	}
	if n := f.Count(t, "SELECT count(*) FROM agent_task_queue WHERE context->>'project_id'=$1", util.UUIDToString(p.ID)); n != 3 {
		t.Fatalf("queued %d runs", n)
	}
}

func TestProjectSupervisionStagesRespectPolicyCancellationAndDependencies(t *testing.T) {
	f, s, p, _ := supervisionFixture(t)
	ctx := context.Background()
	rt := f.Runtime(t, "stage worker")
	worker := f.Agent(t, "stage worker", rt)
	parent := f.Issue(t, "root", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "in_progress", "assignee_type": "member", "assignee_id": f.UserID})
	previous := f.Issue(t, "first stage", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "done", "parent_issue_id": parent, "stage": 1})
	next := f.Issue(t, "second stage", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "backlog", "parent_issue_id": parent, "stage": 2, "assignee_type": "agent", "assignee_id": worker})
	f.Cleanup(t, "DELETE FROM agent_task_queue WHERE issue_id=$1", next)
	view := supervisionCheck(t, s, p)
	if view.LastTaskID != nil {
		t.Fatal("intentional backlog woke lead before opt-in")
	}
	config := view.Config
	config.AutoAdvance = true
	if err := s.Save(ctx, p, parseTestUUID(t, f.UserID), true, config, view.Revision); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, "UPDATE issue SET status='cancelled' WHERE id=$1", previous)
	view = supervisionCheck(t, s, p)
	if view.Snapshot.Counts.Blocked != 1 || view.LastTaskID == nil {
		t.Fatalf("cancelled stage decision missing: %+v", view)
	}
	if count := f.Count(t, "SELECT count(*) FROM agent_task_queue WHERE issue_id=$1", next); count != 0 {
		t.Fatal("cancelled stage advanced")
	}
	f.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE context->>'project_id'=$1", util.UUIDToString(p.ID))
	f.Exec(t, "UPDATE issue SET status='done' WHERE id=$1", previous)
	view = supervisionCheck(t, s, p)
	if view.LastReason != "work_released" {
		t.Fatalf("stage not released: %+v", view)
	}
	issue, err := f.q.GetIssue(ctx, parseTestUUID(t, next))
	if err != nil || issue.Status != "todo" {
		t.Fatalf("stage status: %s %v", issue.Status, err)
	}
	blocked := f.Issue(t, "blocked on cancelled work", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "todo", "assignee_type": "agent", "assignee_id": worker})
	cancelled := f.Issue(t, "cancelled dependency", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "cancelled"})
	f.Insert(t, "issue_dependency", testutil.Cols{"issue_id": cancelled, "depends_on_issue_id": blocked, "type": "blocks"})
	view, err = s.View(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range view.Snapshot.Issues {
		if item.ID == blocked && item.Reason != "dependency" {
			t.Fatalf("inverse cancelled dependency escaped: %+v", item)
		}
	}
}

func TestProjectSupervisionRuntimeCapacityAndAuthorityRevocation(t *testing.T) {
	f, s, p, _ := supervisionFixture(t)
	ctx := context.Background()
	rt := f.Runtime(t, "limited", testutil.Cols{"metadata": testutil.Raw(`'{"execution_slots":1}'::jsonb`)})
	worker := f.Agent(t, "limited worker", rt, testutil.Cols{"max_concurrent_tasks": 3})
	running := f.Task(t, worker, testutil.Cols{"runtime_id": rt, "status": "running"})
	issue := f.Issue(t, "ready limited", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "todo", "assignee_type": "agent", "assignee_id": worker})
	f.Cleanup(t, "DELETE FROM agent_task_queue WHERE issue_id=$1", issue)
	view := supervisionCheck(t, s, p)
	if view.LastReason != "runtime_capacity" || view.Snapshot.Counts.Blocked != 1 {
		t.Fatalf("runtime capacity ignored: %+v", view)
	}
	f.Exec(t, "UPDATE agent_task_queue SET status='completed' WHERE id=$1", running)
	view = supervisionCheck(t, s, p)
	if view.LastReason != "work_released" {
		t.Fatalf("capacity did not recover: %+v", view)
	}
	f.Issue(t, "new unassigned", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "todo"})
	view = supervisionCheck(t, s, p)
	if view.LastTaskID == nil {
		t.Fatal("no coordinator")
	}
	f.Exec(t, "UPDATE member SET role='member' WHERE user_id=$1 AND workspace_id=$2", f.UserID, f.WorkspaceID)
	task, err := f.q.GetAgentTask(ctx, parseTestUUID(t, *view.LastTaskID))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Tasks.ClaimTask(ctx, task.AgentID)
	if err != nil || claimed != nil {
		t.Fatalf("revoked coordinator admitted: %+v %v", claimed, err)
	}
	task, err = f.q.GetAgentTask(ctx, task.ID)
	if err != nil || task.Status != "cancelled" {
		t.Fatalf("revoked run not settled: %s %v", task.Status, err)
	}
}

func TestProjectSupervisionSharedLeadGetsFairCapacityOpportunity(t *testing.T) {
	f, s, p, lead := supervisionFixture(t)
	ctx := context.Background()
	s.Listen()
	f.Issue(t, "first unassigned", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": "todo"})
	first := supervisionCheck(t, s, p)
	if first.LastTaskID == nil {
		t.Fatal("first coordinator missing")
	}
	otherID := f.Project(t, "waiting project", testutil.Cols{"status": "in_progress", "lead_type": "agent", "lead_id": lead})
	other, err := f.q.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: parseTestUUID(t, otherID), WorkspaceID: p.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	f.Cleanup(t, "DELETE FROM project_supervision WHERE project_id=$1", otherID)
	f.Cleanup(t, "DELETE FROM agent_task_queue WHERE context->>'project_id'=$1", otherID)
	f.Issue(t, "second unassigned", testutil.Cols{"project_id": otherID, "status": "todo"})
	if err = s.Save(ctx, other, parseTestUUID(t, f.UserID), true, DefaultProjectSupervisionConfig(), 0); err != nil {
		t.Fatal(err)
	}
	waiting := supervisionCheck(t, s, other)
	if waiting.LastReason != "lead_capacity" || waiting.LastTaskID != nil {
		t.Fatalf("lead reservation ignored: %+v", waiting)
	}
	f.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", *first.LastTaskID)
	s.Tasks.Bus.Publish(events.Event{Type: "task:completed", WorkspaceID: f.WorkspaceID, Payload: map[string]string{"task_id": *first.LastTaskID}})
	if err = s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	waiting, err = s.View(ctx, other)
	if err != nil || waiting.LastTaskID == nil {
		t.Fatalf("waiting project starved: %+v %v", waiting, err)
	}
	current, err := s.View(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if current.LastTaskID != nil && *current.LastTaskID != *first.LastTaskID {
		t.Fatal("previous project overtook waiting project")
	}
}

func TestProjectSupervisionBoundsRetryAndRepeatedReview(t *testing.T) {
	for _, kind := range []string{"retry", "delegate_review"} {
		t.Run(kind, func(t *testing.T) {
			f, s, p, _ := supervisionFixture(t)
			ctx := context.Background()
			runtime := f.Runtime(t, "worker")
			worker := f.Agent(t, "worker", runtime)
			status := "in_progress"
			runs := 3
			runStatus := "failed"
			handoff := "retry: previous failure"
			if kind == "delegate_review" {
				status = "in_review"
				runs = 1
				runStatus = "completed"
				handoff = "delegate_review: delivered"
			}
			issue := f.Issue(t, "bounded work", testutil.Cols{"project_id": util.UUIDToString(p.ID), "status": status, "assignee_type": "agent", "assignee_id": worker})
			for range runs {
				f.Task(t, worker, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": runStatus, "trigger_evidence_kind": "project_supervision", "handoff_note": handoff, "result": testutil.Raw(`'{"summary":"Delivery recorded"}'::jsonb`)})
			}
			view := supervisionCheck(t, s, p)
			if view.LastTaskID == nil {
				t.Fatal("coordinator missing")
			}
			task, err := f.q.GetAgentTask(ctx, parseTestUUID(t, *view.LastTaskID))
			if err != nil {
				t.Fatal(err)
			}
			contextData, _ := ProjectCoordination(task)
			f.Exec(t, "UPDATE agent_task_queue SET status='running',started_at=now() WHERE id=$1", task.ID)
			current, err := f.q.GetIssue(ctx, parseTestUUID(t, issue))
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Apply(ctx, p, task.ID, contextData.CheckedVersion, []ProjectSupervisionAction{{Kind: kind, IssueID: issue, Revision: current.Revision, AssigneeType: "agent", AssigneeID: worker, Reason: "Check recovery"}})
			if err == nil || !strings.Contains(err.Error(), "human decision") {
				t.Fatalf("unbounded %s accepted: %v", kind, err)
			}
			if count := f.Count(t, "SELECT count(*) FROM agent_task_queue WHERE issue_id=$1", issue); count != runs {
				t.Fatalf("created repeated work: %d", count)
			}
		})
	}
}
