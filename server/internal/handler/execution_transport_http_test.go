package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestExecutionTransportHTTPProof(t *testing.T) {
	directory := os.Getenv("TASK24_EVIDENCE_DIR")
	if directory == "" {
		t.Skip("explicit owned-fixture evidence directory required")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	var task, workspace, user, listener string
	t.Cleanup(func() {
		if task == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		counts := map[string]int{}
		for _, table := range []string{"task_supplement", "task_supplement_capability", "execution_grant", "task_execution", "project_graph_event"} {
			var count int
			if err := testPool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE task_id=$1", task).Scan(&count); err != nil {
				t.Error(err)
				return
			}
			counts[table] = count
			if count != 0 {
				t.Errorf("fixture rows remain in %s", table)
			}
		}
		raw, _ := json.MarshalIndent(map[string]any{"task_id": task, "workspace_id": workspace, "user_id": user, "listener": listener, "remaining_task_rows": counts, "server_closed": true, "credentials": "private curl stdin only; omitted from transcript"}, "", "  ")
		if err := os.WriteFile(filepath.Join(directory, "cleanup.json"), raw, 0600); err != nil {
			t.Error(err)
		}
	})
	f := newExecutionTransportFixture(t)
	task, workspace, user = f.task, f.workspace, f.user
	server := httptest.NewServer(f.router)
	defer server.Close()
	listener = server.URL
	var transcript bytes.Buffer
	invoke := func(name, method, path, token string, body any, want int, out any) {
		t.Helper()
		payload, _ := json.Marshal(body)
		config := "url = " + strconv.Quote(server.URL+path) + "\nrequest = " + strconv.Quote(method) + "\nheader = " + strconv.Quote("Authorization: Bearer "+token) + "\nheader = \"Content-Type: application/json\"\ndata = " + strconv.Quote(string(payload)) + "\n"
		command := exec.CommandContext(t.Context(), "curl", "-i", "--silent", "--show-error", "--path-as-is", "--max-time", "10", "--config", "-")
		command.Stdin = strings.NewReader(config)
		raw, err := command.Output()
		if err != nil {
			t.Fatalf("curl %s: %v", name, err)
		}
		boundary := bytes.Index(raw, []byte("\r\n\r\n"))
		if boundary < 0 {
			t.Fatal("curl missing HTTP headers")
		}
		headers, content := raw[:boundary], raw[boundary+4:]
		var status int
		if _, err = fmt.Sscanf(string(bytes.SplitN(headers, []byte("\r\n"), 2)[0]), "HTTP/1.1 %d", &status); err != nil {
			t.Fatal(err)
		}
		if status != want {
			t.Fatalf("curl %s status=%d want=%d", name, status, want)
		}
		if out != nil {
			if err = json.Unmarshal(content, out); err != nil {
				t.Fatal(err)
			}
		}
		var sanitized map[string]any
		if json.Unmarshal(content, &sanitized) == nil {
			if _, ok := sanitized["token"]; ok {
				sanitized["token"] = "[REDACTED]"
			}
			content, _ = json.Marshal(sanitized)
		}
		transcript.WriteString("scenario: " + name + "\ncommand: curl -i --path-as-is --max-time 10 --config - [private stdin: " + method + " " + path + "]\n")
		transcript.Write(headers)
		transcript.WriteString("\r\n\r\n")
		transcript.Write(content)
		transcript.WriteString("\n")
	}
	operations := []string{"supplements/claim", "supplements/ack", "worktree-delivery", "project-graph/events"}
	var grant protocol.ExecutionGrantResponse
	invoke("issue exact operation subset", "POST", f.controlPath("/tasks/"+f.task+"/execution-grants"), f.control, protocol.ExecutionGrantRequest{ExecutionID: f.identity.ExecutionID, SupervisorEpoch: 1, Operations: operations}, 200, &grant)
	invoke("claim", "POST", f.path("supplements/claim"), grant.Token, map[string]any{}, 200, nil)
	for i := range 2 {
		invoke(fmt.Sprintf("ack repeat%d", i), "POST", f.path("supplements/ack"), grant.Token, f.body("supplements/ack"), 200, nil)
	}
	for i := range 2 {
		invoke(fmt.Sprintf("worktree repeat%d", i), "POST", f.path("worktree-delivery"), grant.Token, f.body("worktree-delivery"), 200, nil)
	}
	invoke("project graph", "POST", f.path("project-graph/events"), grant.Token, f.body("project-graph/events"), 201, nil)
	invoke("foreign comment", "POST", f.taskPath("supplements/"+uuid.NewString()+"/ack"), grant.Token, map[string]any{"delivered": true}, 409, nil)
	invoke("encoded alias", "POST", f.taskPath("project-graph%2fevents"), grant.Token, f.body("project-graph/events"), 403, nil)
	invoke("account route forbidden", "POST", "/api/me", grant.Token, map[string]any{}, 403, nil)
	var status, branch string
	var attempts int
	f.fx.QueryRow(t, "SELECT status,attempt_count FROM task_supplement WHERE task_id=$1 AND comment_id=$2", f.task, f.comment).Scan(&status, &attempts)
	f.fx.QueryRow(t, "SELECT branch_name FROM agent_task_queue WHERE id=$1", f.task).Scan(&branch)
	graphCount := f.fx.Count(t, "SELECT count(*) FROM project_graph_event WHERE task_id=$1 AND workspace_id=$2 AND project_id=$3", f.task, f.workspace, f.project)
	if status != "delivered" || attempts != 1 || branch != "agent/task24" || graphCount != 1 {
		t.Fatal("owned database readback differs")
	}
	f.fx.Exec(t, "UPDATE task_execution SET revoked=true WHERE task_id=$1", f.task)
	invoke("revoked execution", "POST", f.path("worktree-delivery"), grant.Token, f.body("worktree-delivery"), 401, nil)
	server.Close()
	connection, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), 100*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("HTTP fixture listener remains open")
	}
	if err = os.WriteFile(filepath.Join(directory, "http-surface.log"), transcript.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	proof, _ := json.MarshalIndent(map[string]any{"task_id": f.task, "execution_id": f.identity.ExecutionID, "runtime_id": f.runtime, "project_id": f.project, "workspace_id": f.workspace, "supplement_status": status, "supplement_attempts": attempts, "branch": branch, "graph_rows": graphCount, "listener_closed": true, "real_accounts": false}, "", "  ")
	if err = os.WriteFile(filepath.Join(directory, "http-readback.json"), proof, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("literal curl scoped routes + owned DB: supplement attempts1 delivered, duplicate worktree stable, graph rows1, foreign/encoded/account/revoked rejected; listener closed")
}
