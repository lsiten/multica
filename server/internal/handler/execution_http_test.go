package handler

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestExecutionHTTPProof exercises literal authenticated HTTP through curl,
// including grant issuance, callbacks, and reconcile, with database readback.
func TestExecutionHTTPProof(t *testing.T) {
	f := newExecutionFixture(t)
	server := httptest.NewServer(f.router)
	defer server.Close()
	type observation struct {
		Method, Path string
		Status       int
		Body         json.RawMessage
	}
	evidence := struct {
		Invocation  string        `json:"invocation"`
		Listener    string        `json:"listener"`
		RuntimeID   string        `json:"runtime_id"`
		TaskID      string        `json:"task_id"`
		ExecutionID string        `json:"execution_id"`
		Before      string        `json:"before"`
		After       string        `json:"after"`
		Requests    []observation `json:"requests"`
		Cleanup     string        `json:"cleanup"`
	}{Invocation: "curl --silent --show-error --config - --write-out '\\n%{http_code}' (credential and JSON body on private stdin)", Listener: server.URL, RuntimeID: f.runtime, TaskID: f.task, ExecutionID: f.identity.ExecutionID, Cleanup: "httptest server.Close and fixture cleanup after this test"}
	f.fx.QueryRow(t, "SELECT status FROM agent_task_queue WHERE id=$1", f.task).Scan(&evidence.Before)
	request := func(path, token string, body any, want int, out any) {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		config := "url = " + strconv.Quote(server.URL+path) + "\nrequest = \"POST\"\nheader = " + strconv.Quote("Authorization: Bearer "+token) + "\nheader = \"Content-Type: application/json\"\ndata = " + strconv.Quote(string(payload)) + "\n"
		command := exec.Command("curl", "--silent", "--show-error", "--config", "-", "--write-out", "\n%{http_code}")
		command.Stdin = strings.NewReader(config)
		raw, err := command.Output()
		if err != nil {
			t.Fatalf("curl request failed: %v", err)
		}
		split := strings.LastIndexByte(string(raw), '\n')
		if split < 0 {
			t.Fatal("curl omitted status")
		}
		status, err := strconv.Atoi(string(raw[split+1:]))
		if err != nil {
			t.Fatal(err)
		}
		response := raw[:split]
		if status != want {
			t.Fatalf("curl %s status=%d want=%d", path, status, want)
		}
		if out != nil {
			if err = json.Unmarshal(response, out); err != nil {
				t.Fatal(err)
			}
		}
		if strings.Contains(path, "execution-grants") && status == 200 {
			var redacted map[string]any
			if err = json.Unmarshal(response, &redacted); err != nil {
				t.Fatal(err)
			}
			redacted["token"] = "REDACTED"
			response, err = json.Marshal(redacted)
			if err != nil {
				t.Fatal(err)
			}
		}
		evidence.Requests = append(evidence.Requests, observation{"POST", path, status, response})
	}
	var supervisor protocol.SupervisorResponse
	request(f.controlPath("/execution-supervisor"), f.control, protocol.SupervisorRequest{InstanceID: f.instance}, 200, &supervisor)
	var identity protocol.ExecutionIdentity
	request(f.controlPath("/tasks/"+f.task+"/execution"), f.control, protocol.BindExecutionRequest{WorkerID: f.identity.WorkerID, DispatchedAt: f.claim, SupervisorEpoch: 1}, 200, &identity)
	var grant protocol.ExecutionGrantResponse
	request(f.controlPath("/tasks/"+f.task+"/execution-grants"), f.control, f.grantRequest(), 200, &grant)
	request(f.taskPath("start"), grant.Token, map[string]any{"runtime_id": f.runtime, "dispatched_at": f.claim}, 200, nil)
	snapshot := protocol.ReconcileExecutionsRequest{SnapshotID: uuid.NewString(), SupervisorEpoch: 1, Complete: true, Entries: []protocol.ExecutionObservation{{ExecutionIdentity: f.identity, State: "live"}}}
	var reconciliation protocol.ReconcileExecutionsResponse
	request(f.controlPath("/executions/reconcile"), f.control, snapshot, 200, &reconciliation)
	if reconciliation.Results[0].Outcome != "adopt" {
		t.Fatal("HTTP reconcile did not adopt")
	}
	request(f.taskPath("complete"), grant.Token, TaskCompleteRequest{Output: "literal HTTP evidence"}, 200, nil)
	request(f.taskPath("usage"), grant.Token, map[string]any{"usage": []TaskUsagePayload{{Provider: "fake", Model: "http-proof", InputTokens: 11}}}, 200, nil)
	request(f.taskPath("complete"), f.control, TaskCompleteRequest{Output: "legacy must not overwrite"}, 409, nil)
	f.fx.QueryRow(t, "SELECT status FROM agent_task_queue WHERE id=$1", f.task).Scan(&evidence.After)
	var output string
	f.fx.QueryRow(t, "SELECT result->>'output' FROM agent_task_queue WHERE id=$1", f.task).Scan(&output)
	if evidence.Before != "dispatched" || evidence.After != "completed" || output != "literal HTTP evidence" {
		t.Fatalf("HTTP DB state: %s -> %s output=%q", evidence.Before, evidence.After, output)
	}
	if count := f.fx.Count(t, "SELECT count(*) FROM task_usage WHERE task_id=$1 AND input_tokens=11", f.task); count != 1 {
		t.Fatal("HTTP usage missing")
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if directory := os.Getenv("MULTICA_EXECUTION_EVIDENCE_DIR"); directory != "" {
		if err = os.WriteFile(filepath.Join(directory, "http-proof.json"), encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Log(fmt.Sprintf("literal HTTP proof: %d requests, task=%s %s -> %s, grant redacted", len(evidence.Requests), f.task, evidence.Before, evidence.After))
}
