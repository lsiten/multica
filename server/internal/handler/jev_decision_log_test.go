package handler

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func jevAuditFixture(t *testing.T) (string, string) {
	t.Helper()
	agentID := dbfx.Agent(t, "Audit reviewer "+uuid.NewString(), testRuntimeID)
	issueID := dbfx.Issue(t, "Jev audit task")
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": testRuntimeID, "issue_id": issueID, "status": "running"})
	dbfx.Cleanup(t, `DELETE FROM jev_decision_log WHERE task_id=$1`, taskID)
	return taskID, agentID
}

func jevAuditReport(id string, started time.Time, result string) protocol.JevDecisionLog {
	completed := started.Add(time.Second)
	report := protocol.JevDecisionLog{ID: id, Tool: "multica_jev_systemone", Source: "local", Model: "Mapika/decider-2b", StartedAt: started, CompletedAt: &completed, DurationMS: 1000, ResultClass: result, Input: `{"question":"report evidence"}`, Output: `{"verdict":"yes"}`, Requests: []protocol.JevDecisionRequestLog{}}
	if result == "running" {
		report.CompletedAt = nil
		report.DurationMS = 0
		report.Output = ""
	}
	return report
}

func uploadJevAudit(t *testing.T, taskID string, report protocol.JevDecisionLog) {
	t.Helper()
	r := withURLParams(newRequest(http.MethodPost, "/api/daemon/tasks/"+taskID+"/jev-decision-logs", report), "taskId", taskID)
	testutil.Call(t, testHandler.ReportJevDecisionLog, r).Want(200)
}

func jevAuditRead(method, path string) *http.Request {
	return withURLParams(newRequest(method, path, nil), "id", testWorkspaceID)
}

func TestJevDecisionLogUploadIsIdempotentAndCannotRegress(t *testing.T) {
	taskID, agentID := jevAuditFixture(t)
	id, started := uuid.NewString(), time.Now().UTC().Add(-time.Minute)
	complete := jevAuditReport(id, started, "success")
	uploadJevAudit(t, taskID, complete)
	uploadJevAudit(t, taskID, jevAuditReport(id, started, "running"))
	uploadJevAudit(t, taskID, complete)
	var detail struct {
		protocol.JevDecisionLog
		TaskID    string `json:"task_id"`
		AgentID   string `json:"agent_id"`
		AgentName string `json:"agent_name"`
	}
	r := withURLParams(jevAuditRead(http.MethodGet, "/jev-decision-logs/"+id), "decisionId", id)
	testutil.Call(t, testHandler.GetJevDecisionLog, r).Want(200).JSON(&detail)
	if detail.TaskID != taskID || detail.AgentID != agentID || !strings.HasPrefix(detail.AgentName, "Audit reviewer ") || detail.ResultClass != "success" || detail.Input != complete.Input {
		t.Fatalf("stored audit lost its resolved owner or regressed: %+v", detail)
	}
	otherTask, _ := jevAuditFixture(t)
	testutil.Call(t, testHandler.ReportJevDecisionLog, withURLParams(newRequest(http.MethodPost, "/jev-decision-logs", complete), "taskId", otherTask)).Want(409)
}

func TestJevDecisionLogPaginationSearchAndFilters(t *testing.T) {
	taskID, _ := jevAuditFixture(t)
	started := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	for index, result := range []string{"success", "error", "rejected"} {
		uploadJevAudit(t, taskID, jevAuditReport(uuid.NewString(), started.Add(time.Duration(index)*time.Minute), result))
	}
	type logPage struct {
		Items []jevDecisionSummary `json:"items"`
		Total int                  `json:"total"`
		AsOf  string               `json:"as_of"`
	}
	var first, second logPage
	testutil.Call(t, testHandler.ListJevDecisionLogs, jevAuditRead(http.MethodGet, "/jev-decision-logs?limit=1&agent=Audit%20reviewer")).Want(200).JSON(&first)
	if first.Total != 3 || len(first.Items) != 1 || first.Items[0].ResultClass != "rejected" {
		t.Fatalf("invalid first page: %+v", first)
	}
	uploadJevAudit(t, taskID, jevAuditReport(uuid.NewString(), started.Add(3*time.Minute), "success"))
	testutil.Call(t, testHandler.ListJevDecisionLogs, jevAuditRead(http.MethodGet, "/jev-decision-logs?limit=1&offset=1&agent=Audit%20reviewer&as_of="+first.AsOf)).Want(200).JSON(&second)
	if second.Total != 3 || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.Items[0].ResultClass != "error" {
		t.Fatalf("pagination snapshot changed: %+v", second)
	}
	for _, query := range []string{"q=report&result_class=error&source=local", "agent=Audit%20reviewer&result_class=error", "from=" + started.Add(time.Minute).Format(time.RFC3339) + "&to=" + started.Add(time.Minute).Format(time.RFC3339)} {
		var page logPage
		testutil.Call(t, testHandler.ListJevDecisionLogs, jevAuditRead(http.MethodGet, "/jev-decision-logs?"+query)).Want(200).JSON(&page)
		if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ResultClass != "error" {
			t.Fatalf("filter %q did not match the decision: %+v", query, page)
		}
	}
}

func TestJevDecisionLogsRejectInvalidAndUnauthorizedRequests(t *testing.T) {
	taskID, _ := jevAuditFixture(t)
	for _, query := range []string{"limit=0", "limit=101", "offset=-1", "source=invalid", "result_class=invalid", "from=invalid", "q=" + strings.Repeat("x", 501)} {
		testutil.Call(t, testHandler.ListJevDecisionLogs, jevAuditRead(http.MethodGet, "/jev-decision-logs?"+query)).Want(400)
	}
	r := jevAuditRead(http.MethodGet, "/jev-decision-logs")
	r.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.ListJevDecisionLogs, r).Want(403)
	r = withURLParams(jevAuditRead(http.MethodGet, "/jev-decision-logs/missing"), "decisionId", uuid.NewString())
	testutil.Call(t, testHandler.GetJevDecisionLog, r).Want(404)
	for _, body := range []string{`{"id":"bad"}`, `{"id":"bad","agent_id":"spoofed"}`, `null`, `{}`} {
		r := withURLParams(newRequest(http.MethodPost, "/jev-decision-logs", body), "taskId", taskID)
		testutil.Call(t, testHandler.ReportJevDecisionLog, r).Want(400)
	}
}

func TestJevDecisionLogsStayWithinWorkspaceAndAdminScope(t *testing.T) {
	taskID, _ := jevAuditFixture(t)
	id := uuid.NewString()
	uploadJevAudit(t, taskID, jevAuditReport(id, time.Now().UTC().Add(-time.Minute), "success"))
	memberID := dbfx.Insert(t, "user", testutil.Cols{"name": "Log viewer", "email": uuid.NewString() + "@example.test"})
	dbfx.Member(t, testWorkspaceID, memberID, "member")
	r := jevAuditRead(http.MethodGet, "/jev-decision-logs")
	r.Header.Set("X-User-ID", memberID)
	testutil.Call(t, testHandler.ListJevDecisionLogs, r).Want(403)
	r = withURLParams(jevAuditRead(http.MethodGet, "/jev-decision-logs/"+id), "decisionId", id)
	r.Header.Set("X-User-ID", memberID)
	testutil.Call(t, testHandler.GetJevDecisionLog, r).Want(403)
	otherWorkspace := dbfx.Insert(t, "workspace", testutil.Cols{"name": "Other audit workspace", "slug": uuid.NewString()})
	dbfx.Member(t, otherWorkspace, testUserID, "owner")
	r = withURLParams(newRequest(http.MethodGet, "/jev-decision-logs/"+id, nil), "id", otherWorkspace, "decisionId", id)
	testutil.Call(t, testHandler.GetJevDecisionLog, r).Want(404)
}
