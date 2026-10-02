package handler

import (
	"github.com/multica-ai/multica/server/internal/testutil"
	"net/http"
	"testing"
)

func TestProjectCollaborationSearchStopsAtInvisibleParent(t *testing.T) {
	viewer := dbfx.User(t, "hierarchy-reader", "hierarchy-reader@example.test")
	dbfx.Member(t, testWorkspaceID, viewer, "member")
	runtime := dbfx.Runtime(t, "hierarchy-private-runtime")
	worker := dbfx.Agent(t, "visible-worker", runtime, testutil.Cols{"owner_id": viewer})
	hidden := dbfx.Agent(t, "hidden-parent-owner", runtime)
	project := dbfx.Project(t, "hidden-hierarchy-search")
	grand := dbfx.Issue(t, "visible-grandparent", testutil.Cols{"project_id": project, "status": "done"})
	parent := dbfx.Issue(t, "hidden-parent", testutil.Cols{"project_id": project, "parent_issue_id": grand, "assignee_type": "agent", "assignee_id": hidden})
	child := dbfx.Issue(t, "visible-child", testutil.Cols{"project_id": project, "parent_issue_id": parent, "assignee_type": "agent", "assignee_id": worker})
	dbfx.Task(t, worker, testutil.Cols{"issue_id": child, "runtime_id": runtime, "status": "completed"})
	var graph projectCollaborationGraph
	testutil.Call(t, testHandler.GetProjectCollaborationGraph, withURLParam(newRequestAs(viewer, http.MethodGet, "/api/projects/"+project+"/collaboration-graph?activity=active&task_query=visible-grandparent", nil), "id", project)).Want(http.StatusOK).JSON(&graph)
	if graph.Summary.TaskCount != 0 {
		t.Fatalf("search inferred lineage through an invisible parent: %+v", graph.Summary)
	}
}

func TestProjectCollaborationTaskTreeFiltersAndArtifacts(t *testing.T) {
	runtime := dbfx.Runtime(t, "collaboration-complete-runtime")
	lead := dbfx.Agent(t, "collaboration-lead", runtime)
	worker := dbfx.Agent(t, "collaboration-worker", runtime)
	project := dbfx.Project(t, "collaboration-task-tree")
	parent := dbfx.Issue(t, "parent review", testutil.Cols{"project_id": project, "status": "done", "assignee_type": "agent", "assignee_id": lead})
	child := dbfx.Issue(t, "unfinished child", testutil.Cols{"project_id": project, "status": "in_review", "parent_issue_id": parent, "assignee_type": "agent", "assignee_id": worker})
	source := dbfx.Task(t, lead, testutil.Cols{"issue_id": parent, "status": "completed", "runtime_id": runtime})
	first := dbfx.Task(t, worker, testutil.Cols{"issue_id": child, "status": "completed", "runtime_id": runtime, "delegated_from_task_id": source})
	second := dbfx.Task(t, worker, testutil.Cols{"issue_id": child, "status": "failed", "runtime_id": runtime, "delegated_from_task_id": source})
	dbfx.Insert(t, "project_graph_event", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": project, "task_id": second, "event_type": "task_failed", "node_id": second})
	file := dbfx.Insert(t, "attachment", testutil.Cols{"workspace_id": testWorkspaceID, "task_id": second, "filename": "review.txt", "url": "/fixture/review.txt", "content_type": "text/plain", "size_bytes": 1, "uploader_type": "member", "uploader_id": testUserID})
	var graph projectCollaborationGraph
	testutil.Call(t, testHandler.GetProjectCollaborationGraph, withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/collaboration-graph?activity=active", nil), "id", project)).Want(http.StatusOK).JSON(&graph)
	if graph.Summary.TaskCount != 1 || graph.Summary.ActiveCount != 1 || graph.Summary.RunCount != 2 || len(graph.Edges) != 1 || graph.Edges[0].Count != 1 {
		t.Fatalf("task/run aggregation: %+v edges=%+v", graph.Summary, graph.Edges)
	}
	var evidence projectCollaborationEvidenceResponse
	testutil.Call(t, testHandler.GetProjectCollaborationEvidence, withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/collaboration-evidence?activity=active&status=failed", nil), "id", project)).Want(http.StatusOK).JSON(&evidence)
	if len(evidence.Evidence) != 1 || evidence.Evidence[0].TaskID != second || evidence.Evidence[0].TaskID == first || len(evidence.Evidence[0].Artifacts) != 1 || evidence.Evidence[0].Artifacts[0].ID != file || len(evidence.Evidence[0].Events) != 1 {
		t.Fatalf("filtered evidence=%+v", evidence.Evidence)
	}
	foundParent := false
	for _, issue := range evidence.Issues {
		if issue.ID == parent {
			foundParent = true
			if !issue.ContextOnly {
				t.Fatal("completed parent must be context only")
			}
		}
	}
	if !foundParent {
		t.Fatalf("missing real parent issue: %+v", evidence.Issues)
	}
	testutil.Call(t, testHandler.GetProjectCollaborationEvidence, withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/collaboration-evidence?activity=active&agent_id="+lead, nil), "id", project)).Want(http.StatusOK).JSON(&evidence)
	if len(evidence.Evidence) != 2 {
		t.Fatalf("source agent selection lost outgoing runs: %+v", evidence.Evidence)
	}
	dbfx.Insert(t, "issue_status", testutil.Cols{"workspace_id": testWorkspaceID, "key": "collaboration_done", "name": "Accepted", "category": "done", "color": "#008800"})
	dbfx.Exec(t, "UPDATE issue SET status='collaboration_done' WHERE id=$1", child)
	testutil.Call(t, testHandler.GetProjectCollaborationGraph, withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/collaboration-graph?activity=active", nil), "id", project)).Want(http.StatusOK).JSON(&graph)
	if graph.Summary.TaskCount != 0 {
		t.Fatalf("ended task still active: %+v", graph.Summary)
	}
}

func TestProjectCollaborationParentChildAndCustomTerminalStatus(t *testing.T) {
	runtime := dbfx.Runtime(t, "parent-child-runtime")
	lead := dbfx.Agent(t, "parent-child-lead", runtime)
	worker := dbfx.Agent(t, "parent-child-worker", runtime)
	project := dbfx.Project(t, "parent-child-project")
	parent := dbfx.Issue(t, "parent without delegation", testutil.Cols{"project_id": project, "status": "done", "assignee_type": "agent", "assignee_id": lead})
	child := dbfx.Issue(t, "child without delegation", testutil.Cols{"project_id": project, "parent_issue_id": parent, "status": "todo", "assignee_type": "agent", "assignee_id": worker})
	dbfx.Task(t, lead, testutil.Cols{"issue_id": parent, "status": "completed", "runtime_id": runtime})
	dbfx.Task(t, worker, testutil.Cols{"issue_id": child, "status": "completed", "runtime_id": runtime})
	var graph projectCollaborationGraph
	testutil.Call(t, testHandler.GetProjectCollaborationGraph, withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/collaboration-graph?activity=active&relation_type=parent_child", nil), "id", project)).Want(http.StatusOK).JSON(&graph)
	if len(graph.Edges) != 1 || graph.Edges[0].Type != "parent_child" || graph.Edges[0].From != "agent:"+lead || graph.Edges[0].To != "agent:"+worker {
		t.Fatalf("parent-child relationship is missing: %+v", graph.Edges)
	}
	var evidence projectCollaborationEvidenceResponse
	testutil.Call(t, testHandler.GetProjectCollaborationEvidence, withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/collaboration-evidence?activity=active&task_query=parent%20without%20delegation", nil), "id", project)).Want(http.StatusOK).JSON(&evidence)
	if len(evidence.Evidence) != 1 || evidence.Evidence[0].SourceTaskID != nil || evidence.Evidence[0].SourceIssueID == nil || *evidence.Evidence[0].SourceIssueID != parent {
		t.Fatalf("structural parent must not invent a source run: %+v", evidence.Evidence)
	}
	dbfx.Exec(t, "UPDATE issue SET status='done' WHERE id=$1", child)
	testutil.Call(t, testHandler.GetProjectCollaborationGraph, withURLParam(newRequest(http.MethodGet, "/api/projects/"+project+"/collaboration-graph?activity=ended", nil), "id", project)).Want(http.StatusOK).JSON(&graph)
	if len(graph.Edges) != 1 || graph.Edges[0].ActiveCount != 0 {
		t.Fatalf("ended relation=%+v", graph.Edges)
	}
}
