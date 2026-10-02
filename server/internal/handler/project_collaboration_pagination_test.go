package handler

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestCollaborationEvidenceCursorPreservesSortAndNodeIntersection(t *testing.T) {
	runtime := dbfx.Runtime(t, "cursor-runtime")
	a := dbfx.Agent(t, "cursor-source", runtime)
	b := dbfx.Agent(t, "cursor-worker", runtime)
	c := dbfx.Agent(t, "cursor-other", runtime)
	project := dbfx.Project(t, "cursor-project")
	issue := dbfx.Issue(t, "cursor-task", testutil.Cols{"project_id": project})
	source := dbfx.Task(t, a, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "completed"})
	first := dbfx.Task(t, b, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "completed", "delegated_from_task_id": source})
	second := dbfx.Task(t, b, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "completed", "delegated_from_task_id": source})
	dbfx.Exec(t, "UPDATE agent_task_queue SET created_at='2020-01-01T00:00:00.100Z' WHERE id=$1", first)
	dbfx.Exec(t, "UPDATE agent_task_queue SET created_at='2020-01-01T00:00:00.200Z' WHERE id=$1", second)
	dbfx.Task(t, c, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "completed", "delegated_from_task_id": second})
	base := "/api/projects/" + project + "/collaboration-evidence?activity=active&agent_id=" + a + "&node_agent_id=" + b + "&sort=oldest&cursor_mode=true&limit=1"
	var page projectCollaborationEvidenceResponse
	testutil.Call(t, testHandler.GetProjectCollaborationEvidence, withURLParam(newRequest(http.MethodGet, base, nil), "id", project)).Want(http.StatusOK).JSON(&page)
	if len(page.Evidence) != 1 || page.Evidence[0].TaskID != first || page.Total != 2 || page.NextCursor == nil {
		t.Fatalf("oldest/intersection: %+v", page)
	}
	if page.Evidence[0].SourceIssueID == nil || *page.Evidence[0].SourceIssueID != issue {
		t.Fatalf("source issue missing: %+v", page.Evidence[0])
	}
	inserted := dbfx.Task(t, b, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "completed", "delegated_from_task_id": source})
	dbfx.Exec(t, "UPDATE agent_task_queue SET created_at='2020-01-01T00:00:00.050Z' WHERE id=$1", inserted)
	next := base + "&cursor=" + url.QueryEscape(*page.NextCursor) + "&snapshot_at=" + url.QueryEscape(page.AsOf)
	testutil.Call(t, testHandler.GetProjectCollaborationEvidence, withURLParam(newRequest(http.MethodGet, next, nil), "id", project)).Want(http.StatusOK).JSON(&page)
	if len(page.Evidence) != 1 || page.Evidence[0].TaskID != second || page.HasMore {
		t.Fatalf("cursor duplicated or skipped remaining run: %+v", page)
	}
	var lookup projectCollaborationEvidenceResponse
	testutil.Call(t, testHandler.GetProjectCollaborationEvidence, withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/collaboration-evidence?activity=all&run_id="+source, nil), "id", project)).Want(http.StatusOK).JSON(&lookup)
	if len(lookup.Evidence) != 1 || lookup.Evidence[0].TaskID != source {
		t.Fatalf("source lookup: %+v", lookup)
	}
}

func TestCollaborationCompleteGraphReturnsOneAggregateSnapshot(t *testing.T) {
	runtime := dbfx.Runtime(t, "complete-graph-runtime")
	lead := dbfx.Agent(t, "complete-graph-lead", runtime)
	project := dbfx.Project(t, "complete-graph-project")
	issue := dbfx.Issue(t, "complete-graph-task", testutil.Cols{"project_id": project})
	source := dbfx.Task(t, lead, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "completed"})
	for index := 0; index < 101; index++ {
		worker := dbfx.Agent(t, fmt.Sprintf("complete-graph-%d", index), runtime)
		dbfx.Task(t, worker, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "completed", "delegated_from_task_id": source})
	}
	var graph projectCollaborationGraph
	testutil.Call(t, testHandler.GetProjectCollaborationGraph, withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/collaboration-graph?activity=active&complete=true&limit=1", nil), "id", project)).Want(http.StatusOK).JSON(&graph)
	if len(graph.Edges) != 101 || graph.HasMore || graph.Summary.AgentCount != 102 || graph.Summary.TaskCount != 1 {
		t.Fatalf("inconsistent complete graph: %+v", graph)
	}
}

func TestCollaborationSourceRunLookupAndCursorRespectBoundary(t *testing.T) {
	viewer := dbfx.User(t, "source-run-reader", "source-run-reader@example.test")
	dbfx.Member(t, testWorkspaceID, viewer, "member")
	runtime := dbfx.Runtime(t, "source-run-boundary")
	privateAgent := dbfx.Agent(t, "private-source-run", runtime)
	project := dbfx.Project(t, "source-run-boundary")
	issue := dbfx.Issue(t, "private-source-issue", testutil.Cols{"project_id": project})
	privateRun := dbfx.Task(t, privateAgent, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "completed"})
	var response projectCollaborationEvidenceResponse
	testutil.Call(t, testHandler.GetProjectCollaborationEvidence, withURLParam(newRequestAs(viewer, http.MethodGet, "/api/projects/"+project+"/collaboration-evidence?activity=all&run_id="+privateRun, nil), "id", project)).Want(http.StatusOK).JSON(&response)
	if len(response.Evidence) != 0 || len(response.Issues) != 0 {
		t.Fatalf("private source exposed: %+v", response)
	}
	for _, query := range []string{"cursor=invalid", "snapshot_at=invalid", "sort=invalid", "node_agent_id=invalid"} {
		testutil.Call(t, testHandler.GetProjectCollaborationEvidence, withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/collaboration-evidence?"+query, nil), "id", project)).Want(http.StatusBadRequest)
	}
}
