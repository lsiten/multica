package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestProjectExecutionScopeIncludesProjectLead(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	lead := dbfx.Agent(t, "scope-project-lead", handlerTestRuntimeID(t))
	worker := dbfx.Agent(t, "scope-explicit-worker", handlerTestRuntimeID(t))
	project := dbfx.Project(t, "scope-effective-project", testutil.Cols{"lead_type": "agent", "lead_id": lead})

	request := squadScopeReq("", http.MethodGet, "/api/projects/"+project+"/execution-scope-bindings", nil, map[string]string{"id": project})
	response := httptest.NewRecorder()
	testHandler.GetProjectExecutionScopeBindings(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("get execution scope: status=%d body=%s", response.Code, response.Body.String())
	}
	var initial struct {
		AutoAgentIDs      []string `json:"auto_agent_ids"`
		EffectiveAgentIDs []string `json:"effective_agent_ids"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if len(initial.AutoAgentIDs) != 1 || initial.AutoAgentIDs[0] != lead || !slices.Contains(initial.EffectiveAgentIDs, lead) || !slices.Contains(initial.EffectiveAgentIDs, worker) {
		t.Fatalf("lead scope response = %+v", initial)
	}

	request = squadScopeReq("", http.MethodPut, "/api/projects/"+project+"/execution-scope-bindings", map[string]any{"agent_ids": []string{worker}, "squad_ids": []string{}}, map[string]string{"id": project})
	response = httptest.NewRecorder()
	testHandler.UpdateProjectExecutionScopeBindings(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update execution scope: status=%d body=%s", response.Code, response.Body.String())
	}
	var updated struct {
		AgentIDs          []string `json:"agent_ids"`
		AutoAgentIDs      []string `json:"auto_agent_ids"`
		EffectiveAgentIDs []string `json:"effective_agent_ids"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.AgentIDs) != 1 || updated.AgentIDs[0] != worker || len(updated.AutoAgentIDs) != 1 || !slices.Contains(updated.EffectiveAgentIDs, lead) || !slices.Contains(updated.EffectiveAgentIDs, worker) {
		t.Fatalf("updated scope response = %+v", updated)
	}
}
