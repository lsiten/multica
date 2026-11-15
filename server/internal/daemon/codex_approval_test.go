package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestTaskApprovalRequiresHumanInitiator(t *testing.T) {
	for _, task := range []Task{
		{InitiatorType: "agent", InitiatorID: "agent"},
		{InitiatorType: "member"},
		{InitiatorID: "user"},
	} {
		t.Run(task.InitiatorType+"/"+task.InitiatorID, func(t *testing.T) {
			d := &Daemon{}
			approved, err := d.requestTaskApproval(task)(t.Context(), agent.ApprovalRequest{})
			if approved || err == nil || err.Error() != "daemon: no human approval initiator" {
				t.Fatalf("approval without human initiator: approved=%v err=%v", approved, err)
			}
		})
	}
}

func TestTaskApprovalUsesTaskTokenHumanRequest(t *testing.T) {
	var sent struct {
		Key     string `json:"key"`
		Title   string `json:"title"`
		Details string `json:"details"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
				t.Errorf("decode approval request: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":"request-1","status":"pending","revision":1,"response":null}`))
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"id":"request-1","status":"answered","revision":1,"response":{"revision":1,"decision":"approve"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	d := &Daemon{cfg: Config{ServerBaseURL: server.URL}}
	task := Task{
		ID:            "task-1",
		WorkspaceID:   "workspace-1",
		AgentID:       "agent-1",
		AuthToken:     "mat_task",
		InitiatorType: "member",
		InitiatorID:   "member-1",
	}
	approved, err := d.requestTaskApproval(task)(t.Context(), agent.ApprovalRequest{
		Method: "execCommandApproval",
		Params: json.RawMessage(`{"command":"printf exact"}`),
	})
	if err != nil || !approved {
		t.Fatalf("requestTaskApproval() = %v, %v; want approved", approved, err)
	}
	if sent.Key == "" || sent.Title != "Approve command" || sent.Details != `{"command":"printf exact"}` {
		t.Fatalf("approval request = %+v", sent)
	}
}
