package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func jevLogRecords(t *testing.T, logs *lockedBuffer, message string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid log JSON: %v", err)
		}
		if record["msg"] == message {
			records = append(records, record)
		}
	}
	return records
}

func TestJevDecisionLogsRecordOriginAndInputOutput(t *testing.T) {
	for _, source := range []string{"agent_context", "local", "remote"} {
		t.Run(source, func(t *testing.T) {
			body := `{"choices":[{"message":{"content":"{\"decisions\":[{\"candidate_id\":\"a\",\"verdict\":\"yes\"}]}"}}]}`
			tool := llm2jevMCPToolName
			arguments := any(map[string]any{"question": "Does the report meet the criteria?", "candidates": []map[string]string{{"id": "a", "content": "Report.pdf"}}})
			if source != "agent_context" {
				tool = jevMCPToolName
				body = `{"answers":{"ready":{"type":"noul","noul":0.9}}}`
				arguments = map[string]any{"state": "Report.pdf", "questions": map[string]any{"ready": map[string]any{"type": "noul", "instructions": "Is the report ready?"}}}
			}
			var providerInput []byte
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerInput, _ = io.ReadAll(r.Body)
				_, _ = io.WriteString(w, body)
			}))
			defer model.Close()
			logs := &lockedBuffer{}
			task := Task{
				ID: "decision-run", IssueID: "issue-id", IssueIdentifier: "MUL-123", WorkspaceID: "workspace-id",
				AgentID: "agent-id", RuntimeID: "runtime-id", ProjectID: "project-id", ChatSessionID: "session-id",
				JevConfig: &protocol.WorkspaceJevConfig{Source: source, Revision: 7, ModelRevision: protocol.MapikaDecider2BRevision},
				Agent:     &AgentData{ID: "agent-id", Name: "Reviewer", Model: "decision-model", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL, "OPENAI_API_KEY": "provider-secret"}},
			}
			if source != "agent_context" {
				task.Agent.CustomEnv["MULTICA_JEV_SYSTEMONE"] = "1"
			}
			config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, slog.New(slog.NewJSONHandler(logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{"name": tool, "arguments": arguments})
			if result["isError"] == true {
				t.Fatalf("decision failed: %v", result)
			}
			started := jevLogRecords(t, logs, "jev decision started")
			finished := jevLogRecords(t, logs, "jev decision completed")
			requests := jevLogRecords(t, logs, "llm2jev model request")
			if len(started) != 1 || len(finished) != 1 || len(requests) != 1 {
				t.Fatalf("want one start, completion and model request, got %d/%d/%d", len(started), len(finished), len(requests))
			}
			for _, record := range []map[string]any{started[0], finished[0], requests[0]} {
				for key, want := range map[string]any{"task_id": task.ID, "issue_id": task.IssueID, "issue_identifier": task.IssueIdentifier, "agent_id": task.AgentID, "agent_name": task.Agent.Name, "workspace_id": task.WorkspaceID, "runtime_id": task.RuntimeID, "project_id": task.ProjectID, "chat_session_id": task.ChatSessionID, "source": source, "provider": "claude", "model": task.Agent.Model, "model_revision": task.JevConfig.ModelRevision, "config_revision": float64(7), "tool": tool} {
					if record[key] != want {
						t.Errorf("log %s=%v, want %v", key, record[key], want)
					}
				}
				if record["decision_id"] == "" || record["decision_id"] == nil || record["decision_id"] != started[0]["decision_id"] {
					t.Errorf("decision correlation missing: %v", record["decision_id"])
				}
			}
			var loggedInput any
			if err := json.Unmarshal([]byte(started[0]["input"].(string)), &loggedInput); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(started[0]["input"].(string), "Report.pdf") || finished[0]["result_class"] != "success" || finished[0]["duration_ms"] == nil {
				t.Fatalf("missing decision input/output or duration: %v / %v", started[0], finished[0])
			}
			var loggedOutput map[string]any
			if err := json.Unmarshal([]byte(finished[0]["output"].(string)), &loggedOutput); err != nil || loggedOutput["content"] == nil {
				t.Fatalf("missing returned MCP output: %v", err)
			}
			var loggedProviderOutput, providerOutput any
			if err := json.Unmarshal([]byte(requests[0]["output"].(string)), &loggedProviderOutput); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(body), &providerOutput); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(requests[0]["input"].(string), "Report.pdf") || !reflect.DeepEqual(loggedProviderOutput, providerOutput) || requests[0]["http_status"] != float64(200) || len(providerInput) == 0 {
				t.Fatalf("missing actual model input/output: %v", requests[0])
			}
			if strings.Contains(logs.String(), "provider-secret") || strings.Contains(logs.String(), model.URL) {
				t.Fatal("provider credentials or endpoint leaked into logs")
			}
		})
	}
}

func TestJevDecisionLogsIncludeRejectedAndFailedCalls(t *testing.T) {
	for _, failure := range []string{"invalid_input", "http_error", "invalid_output", "timeout", "call_limit", "concurrency_limit", "completion_binding"} {
		t.Run(failure, func(t *testing.T) {
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				switch failure {
				case "timeout":
					<-r.Context().Done()
				case "http_error":
					w.WriteHeader(http.StatusBadGateway)
					_, _ = io.WriteString(w, `{"message":"provider failed"}`)
				default:
					_, _ = io.WriteString(w, `{"choices":[]}`)
				}
			}))
			defer model.Close()
			logs := &lockedBuffer{}
			task := Task{ID: "real-task", IssueID: "MUL-123", AgentID: "real-agent", Agent: &AgentData{Model: "decision-model", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL}}}
			concurrent, calls := 1, int64(4)
			if failure == "call_limit" {
				calls = 1
			}
			if failure == "concurrency_limit" {
				concurrent = 0
			}
			config, set, err := startTaskLLM2JevMCPAtWithLimits(context.Background(), task.ID, "claude", task, slog.New(slog.NewJSONHandler(logs, nil)), "127.0.0.1", "", concurrent, time.Second, calls)
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			if failure == "timeout" {
				set.server.Handler.(*llm2jevMCPServer).client.Timeout = 20 * time.Millisecond
			}
			params := map[string]any{"name": llm2jevMCPToolName, "arguments": map[string]any{"question": "q", "candidates": []map[string]string{{"id": "a", "content": "x"}}}}
			if failure == "invalid_input" {
				params["arguments"] = map[string]string{"question": "q"}
			}
			if failure == "completion_binding" {
				params = map[string]any{"name": llm2jevMCPCompletionTool, "arguments": map[string]any{"task_id": "spoofed-task", "goal": "MUL-123", "criteria": []string{"report exists"}}}
			}
			if failure == "call_limit" {
				callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{"name": llm2jevMCPToolName, "arguments": map[string]string{"question": "q"}})
			}
			result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", params)
			if result["isError"] != true {
				t.Fatalf("expected failure: %v", result)
			}
			finished := jevLogRecords(t, logs, "jev decision completed")
			if len(finished) == 0 {
				t.Fatal("failed or rejected decision was not logged")
			}
			last := finished[len(finished)-1]
			if last["task_id"] != task.ID || last["agent_id"] != task.AgentID || last["result_class"] == "success" || last["error_code"] == nil || last["output"] == nil {
				t.Fatalf("failure log lost real origin or output: %v", last)
			}
		})
	}
}

func TestJevDecisionLogsRedactCredentialsWithoutChangingRequests(t *testing.T) {
	const providerSecret, taskSecret, daemonSecret, envSecret = "provider-private", "task-private", "daemon-private", "environment-private"
	var received []byte
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "Bearer "+providerSecret {
			t.Error("logging changed the model authentication")
		}
		content, _ := json.Marshal(map[string]any{"decisions": []any{map[string]string{"candidate_id": "a", "verdict": "yes", "reason_code": providerSecret}}})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"api_key": "response-private", "usage": map[string]int{"prompt_tokens": 9, "completion_tokens": 3},
			"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}}},
		})
	}))
	defer model.Close()
	logs := &lockedBuffer{}
	task := Task{ID: "redaction-task", AuthToken: taskSecret, RemoteMCPDaemonToken: daemonSecret, Agent: &AgentData{Model: "model", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL, "OPENAI_API_KEY": providerSecret, "OTHER_SECRET": envSecret}}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{
		"name": llm2jevMCPToolName,
		"arguments": map[string]any{
			"question":   "Check " + taskSecret + " " + daemonSecret + " " + envSecret,
			"evidence":   []string{`{"password":"input-private","nested":{"Authorization":"Bearer nested-private"},"provider-private":"an evidence key"}`},
			"candidates": []map[string]string{{"id": "a", "content": "x"}},
		},
	})
	for _, secret := range []string{providerSecret, taskSecret, daemonSecret, envSecret, "input-private", "nested-private", "response-private", model.URL} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("credential or endpoint %q reached the log", secret)
		}
	}
	if !strings.Contains(string(received), taskSecret) || !strings.Contains(string(received), "input-private") {
		t.Fatal("redaction modified the actual decision inputs")
	}
	requests := jevLogRecords(t, logs, "llm2jev model request")
	if len(requests) != 1 || !strings.Contains(requests[0]["output"].(string), `"prompt_tokens":9`) {
		t.Fatal("redaction hid non-secret token usage")
	}
}

func TestJevDecisionLogsChunkLargeUnicodePayloadWithoutLoss(t *testing.T) {
	logs := &lockedBuffer{}
	decision := &jevDecisionLog{logger: slog.New(slog.NewJSONHandler(logs, nil)).With("decision_id", "large-decision")}
	raw, err := json.Marshal(map[string]string{"evidence": strings.Repeat("报告", jevLogPayloadPartBytes)})
	if err != nil {
		t.Fatal(err)
	}
	attrs := decision.payloadAttrs(decision.logger, "started", "input", raw)
	if attrs[1] != "[chunked]" {
		t.Fatal("large payload was not split")
	}
	parts := jevLogRecords(t, logs, "jev decision payload")
	var reconstructed strings.Builder
	for index, part := range parts {
		payload := part["payload"].(string)
		if part["decision_id"] != "large-decision" || part["part"] != float64(index+1) || part["parts"] != float64(len(parts)) || !utf8.ValidString(payload) || len(payload) > jevLogPayloadPartBytes {
			t.Fatalf("invalid payload part metadata: %v", part)
		}
		reconstructed.WriteString(payload)
	}
	if reconstructed.String() != string(raw) {
		t.Fatal("chunked payload lost or changed content")
	}
}

func TestJevDecisionLogsCorrelateProviderRetries(t *testing.T) {
	var attempts atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"message":"unsupported parameter"}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"decisions\":[{\"candidate_id\":\"a\",\"verdict\":\"yes\"}]}"}}]}`)
	}))
	defer model.Close()
	logs := &lockedBuffer{}
	task := Task{ID: "retry-task", Agent: &AgentData{Model: "model", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL}}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{"name": llm2jevMCPToolName, "arguments": map[string]any{"question": "q", "candidates": []map[string]string{{"id": "a", "content": "x"}}}})
	requests := jevLogRecords(t, logs, "llm2jev model request")
	finished := jevLogRecords(t, logs, "jev decision completed")
	if len(requests) != 3 || len(finished) != 1 || finished[0]["provider_attempts"] != float64(3) {
		t.Fatalf("retry counts do not match: %d / %v", len(requests), finished)
	}
	for index, request := range requests {
		if request["provider_attempt"] != float64(index+1) || request["decision_id"] != finished[0]["decision_id"] || request["output"] == nil || request["input"] == nil {
			t.Fatalf("uncorrelated provider retry: %v", request)
		}
	}
}

func TestJevDecisionLogsKeepConcurrentTasksAndAgentsSeparate(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"decisions\":[{\"candidate_id\":\"a\",\"verdict\":\"yes\"}]}"}}]}`)
	}))
	defer model.Close()
	logs := &lockedBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	var pending sync.WaitGroup
	for _, id := range []string{"task-a", "task-b"} {
		task := Task{ID: id, AgentID: "agent-" + id, Agent: &AgentData{Model: "model", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL}}}
		config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, logger)
		if err != nil {
			t.Fatal(err)
		}
		defer set.Close()
		endpoint := llm2jevMCPURL(t, config)
		for range 2 {
			pending.Add(1)
			go func() {
				defer pending.Done()
				callLLM2JevMCP(t, endpoint, "tools/call", map[string]any{"name": llm2jevMCPToolName, "arguments": map[string]any{"question": id, "candidates": []map[string]string{{"id": "a", "content": "x"}}}})
			}()
		}
	}
	pending.Wait()
	started := jevLogRecords(t, logs, "jev decision started")
	finished := jevLogRecords(t, logs, "jev decision completed")
	if len(started) != 4 || len(finished) != 4 {
		t.Fatalf("missing concurrent decisions: %d / %d", len(started), len(finished))
	}
	origins := make(map[string]string)
	for _, record := range started {
		id := record["decision_id"].(string)
		if origins[id] != "" {
			t.Fatal("two decisions shared the same decision ID")
		}
		origins[id] = record["task_id"].(string)
	}
	for _, record := range finished {
		taskID := origins[record["decision_id"].(string)]
		if taskID == "" || taskID != record["task_id"] || record["agent_id"] != "agent-"+taskID || record["result_class"] != "success" {
			t.Fatalf("decision was associated with the wrong task/agent: %v", record)
		}
	}
}

func TestJevDecisionLogsRecordCompletionVerification(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "semantic", true: "systemone"}[native], func(t *testing.T) {
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if native {
					_, _ = io.WriteString(w, `{"answers":{"completion":{"type":"noul","noul":0.9}}}`)
					return
				}
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"verdict\":\"satisfied\",\"missing\":[]}"}}]}`)
			}))
			defer model.Close()
			logs := &lockedBuffer{}
			task := Task{ID: "verification-task", IssueIdentifier: "MUL-123", AgentID: "verifier-agent", Agent: &AgentData{Model: "model", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL}}}
			if native {
				task.Agent.CustomEnv["MULTICA_JEV_SYSTEMONE"] = "1"
			}
			config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, slog.New(slog.NewJSONHandler(logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{"name": llm2jevMCPCompletionTool, "arguments": map[string]any{"task_id": task.ID, "goal": "Finish MUL-123", "criteria": []string{"report exists"}, "evidence": []string{"Report.pdf"}}})
			finished := jevLogRecords(t, logs, "jev decision completed")
			if result["isError"] == true || len(finished) != 1 || finished[0]["tool"] != llm2jevMCPCompletionTool || !strings.Contains(finished[0]["output"].(string), "satisfied") || finished[0]["agent_id"] != task.AgentID {
				t.Fatalf("completion verification was not logged: %v / %v", result, finished)
			}
		})
	}
}
