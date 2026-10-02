package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/llm2jev"
)

func TestLLM2JevErrorCodesClassifyProviderFailures(t *testing.T) {
	cases := map[string]string{
		"credential_rejected_or_missing": "jev: credential rejected",
		"provider_unreachable":           "llm2jev: model request failed: dial tcp: connection refused",
		"provider_timeout":               "llm2jev: model request failed: context deadline exceeded",
		"provider_http_error":            "llm2jev: model returned HTTP 500",
		"provider_invalid_response":      "llm2jev: model returned invalid JSON",
	}
	for want, message := range cases {
		if got := llm2jevErrorCode(errors.New(message)); got != want {
			t.Errorf("llm2jevErrorCode(%q) = %q, want %q", message, got, want)
		}
	}
}

func llm2jevMCPURL(t *testing.T, config json.RawMessage) string {
	t.Helper()
	var document struct {
		MCPServers map[string]struct {
			URL string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(config, &document); err != nil {
		t.Fatal(err)
	}
	server, ok := document.MCPServers[llm2jevMCPName]
	if !ok || server.URL == "" {
		t.Fatalf("missing %s MCP server in %s", llm2jevMCPName, config)
	}
	return server.URL
}

func callLLM2JevMCP(t *testing.T, endpoint, method string, params any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(endpoint, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing MCP result: %v", body)
	}
	return result
}

func TestLLM2JevMCPUsesAgentRuntimeAndNormalizesSemanticDecision(t *testing.T) {
	var modelCalls int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Fatalf("unexpected model request: path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Contains(body, []byte(`"model":"fixture-model"`)) {
			t.Fatalf("model request did not contain the Agent model: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		content := `{"decisions":[{"candidate_id":"a","verdict":"yes","reason_code":"evidence_match"},{"candidate_id":"b","verdict":"uncertain","reason_code":"missing_evidence"}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
	}))
	defer model.Close()

	task := Task{ID: "decision-task", Agent: &AgentData{
		Model: "fixture-model",
		CustomEnv: map[string]string{
			"OPENAI_BASE_URL": model.URL,
			"OPENAI_API_KEY":  "fixture",
		},
	}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, nil)
	if err != nil {
		t.Fatal(err)
	}
	if set == nil {
		t.Fatal("expected a task-scoped MCP server")
	}
	defer set.Close()

	result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{
		"name": llm2jevMCPToolName,
		"arguments": map[string]any{
			"question":   "Which candidates satisfy the evidence?",
			"evidence":   []string{"a is supported", "b lacks evidence"},
			"candidates": []map[string]string{{"id": "a", "content": "supported"}, {"id": "b", "content": "unknown"}},
		},
	})
	if result["isError"] == true {
		t.Fatalf("decision failed: %v", result)
	}
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	var decision map[string]any
	if err := json.Unmarshal([]byte(text), &decision); err != nil {
		t.Fatal(err)
	}
	if decision["mode"] != "semantic_runtime" || decision["calibrated"] != false {
		t.Fatalf("unexpected decision metadata: %v", decision)
	}
	if len(decision["decisions"].([]any)) != 2 || modelCalls != 1 {
		t.Fatalf("decisions=%v model_calls=%d", decision["decisions"], modelCalls)
	}
}

func TestJevSystemOneMCPUsesTypedProtocol(t *testing.T) {
	var got map[string]any
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Fatalf("unexpected systemone request: path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"route":{"type":"choice","choice":"yes","probabilities":{"yes":0.99,"no":0.01}},"priority":{"type":"score","score":1.4,"probabilities":{"0":0.1,"1":0.8,"2":0.1}}}}`))
	}))
	defer model.Close()
	task := Task{ID: "systemone-task", Agent: &AgentData{Model: "Mapika/decider-2b", CustomEnv: map[string]string{
		"OPENAI_BASE_URL": model.URL, "OPENAI_API_KEY": "fixture", "MULTICA_JEV_SYSTEMONE": "1",
	}}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{
		"name": jevMCPToolName,
		"arguments": map[string]any{
			"state": map[string]any{"text": "task evidence"},
			"questions": map[string]any{
				"route":    map[string]any{"type": "choice", "instructions": "route", "criteria": map[string]any{"yes": "yes", "no": "no"}},
				"priority": map[string]any{"type": "score", "instructions": "priority", "criteria": []string{"low", "medium", "high"}},
			},
		},
	})
	if result["isError"] == true {
		t.Fatalf("systemone decision failed: %v", result)
	}
	if got["state"] == nil || got["questions"] == nil {
		t.Fatalf("typed request missing state/questions: %v", got)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	var response map[string]any
	if err := json.Unmarshal([]byte(text), &response); err != nil || response["answers"] == nil {
		t.Fatalf("invalid systemone result: %s", text)
	}
}

func TestLLM2JevMCPVerifiesCompletionCriteria(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"{\\\"verdict\\\":\\\"incomplete\\\",\\\"missing\\\":[\\\"tests pass\\\"],\\\"reason_code\\\":\\\"missing_evidence\\\"}\"}}]}"))
	}))
	defer model.Close()
	task := Task{ID: "completion-task", Agent: &AgentData{Model: "fixture", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL, "OPENAI_API_KEY": "fixture"}}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{
		"name": llm2jevMCPCompletionTool,
		"arguments": map[string]any{
			"task_id":  "completion-task",
			"goal":     "finish the report",
			"criteria": []string{"report exists", "tests pass"},
			"evidence": []string{"report.md"},
		},
	})
	if result["isError"] == true {
		t.Fatalf("completion verification failed: %v", result)
	}
	var completion map[string]any
	if err := json.Unmarshal([]byte(result["content"].([]any)[0].(map[string]any)["text"].(string)), &completion); err != nil {
		t.Fatal(err)
	}
	if completion["verdict"] != "incomplete" || completion["mode"] != "semantic_runtime" {
		t.Fatalf("unexpected completion result: %v", completion)
	}
}

func TestLLM2JevMCPRejectsUnboundCompletionGoal(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unbound completion goal reached model")
	}))
	defer model.Close()
	task := Task{ID: "bound-goal", IssueIdentifier: "MUL-42", Agent: &AgentData{Model: "fixture", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL}}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{
		"name": llm2jevMCPCompletionTool,
		"arguments": map[string]any{
			"task_id":  task.ID,
			"goal":     "finish an unrelated report",
			"criteria": []string{"report exists"},
		},
	})
	if result["isError"] != true {
		t.Fatalf("unbound goal accepted: %v", result)
	}
}

func TestJevSystemOneMCPVerifiesBoundCompletion(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request llm2jev.SystemOneRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if _, ok := request.Questions["completion"]; !ok {
			t.Fatalf("missing completion question: %+v", request.Questions)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"completion":{"type":"noul","noul":0.9}}}`))
	}))
	defer model.Close()
	task := Task{ID: "systemone-completion", Agent: &AgentData{Model: "decider", CustomEnv: map[string]string{
		"OPENAI_BASE_URL": model.URL, "OPENAI_API_KEY": "fixture", "MULTICA_JEV_SYSTEMONE": "1",
	}}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{
		"name": llm2jevMCPCompletionTool,
		"arguments": map[string]any{
			"task_id":  task.ID,
			"goal":     "finish report",
			"criteria": []string{"report exists"},
			"evidence": []string{"report.md"},
		},
	})
	if result["isError"] == true {
		t.Fatalf("systemone completion failed: %v", result)
	}
	var completion map[string]any
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if err := json.Unmarshal([]byte(text), &completion); err != nil {
		t.Fatal(err)
	}
	if completion["verdict"] != "satisfied" || completion["mode"] != "system_one" {
		t.Fatalf("unexpected completion: %v", completion)
	}
	if confidence, ok := completion["confidence"].(float64); !ok || confidence != 0.9 {
		t.Fatalf("systemone confidence = %#v", completion["confidence"])
	}
	probabilities, ok := completion["probabilities"].(map[string]any)
	satisfied, satisfiedOK := probabilities["satisfied"].(float64)
	incomplete, incompleteOK := probabilities["incomplete"].(float64)
	if !ok || !satisfiedOK || !incompleteOK || satisfied < 0.899 || incomplete < 0.099 || incomplete > 0.101 {
		t.Fatalf("systemone probabilities = %#v", completion["probabilities"])
	}
	if got := set.completionVerification(); !got.Verified {
		t.Fatalf("completion gate not verified: %+v", got)
	}
}

func TestCompletionGateDoesNotAllowStaleAttempt(t *testing.T) {
	gate := newCompletionGate("task")
	first := gate.begin()
	second := gate.begin()
	gate.recordForAttempt(first, "task", "goal", []string{"criterion"}, []string{"evidence"}, "satisfied")
	if got := gate.status(); got.Verified {
		t.Fatalf("stale attempt restored success: %+v", got)
	}
	gate.fail(second, "verification_call_failed")
	if got := gate.status(); got.Verified || got.Reason != "verification_call_failed" {
		t.Fatalf("failed current attempt not fail-closed: %+v", got)
	}
}

func TestLLM2JevMCPRetriesOnlyBadRequestWithCompatibleShape(t *testing.T) {
	var attempts int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		switch attempts {
		case 1:
			if !bytes.Contains(body, []byte("\"max_tokens\":512")) || !bytes.Contains(body, []byte("\"response_format\"")) {
				t.Fatalf("first request was not the primary shape: %s", body)
			}
			http.Error(w, "unsupported max_tokens", http.StatusBadRequest)
		case 2:
			if !bytes.Contains(body, []byte("\"max_tokens\":512")) || bytes.Contains(body, []byte("\"response_format\"")) {
				t.Fatalf("second request was not the legacy compatibility shape: %s", body)
			}
			http.Error(w, "unsupported response_format", http.StatusBadRequest)
		case 3:
			if !bytes.Contains(body, []byte("\"max_completion_tokens\":512")) || !bytes.Contains(body, []byte("\"response_format\"")) {
				t.Fatalf("third request was not the modern compatibility shape: %s", body)
			}
			http.Error(w, "unsupported max_tokens", http.StatusBadRequest)
		default:
			if !bytes.Contains(body, []byte("\"max_completion_tokens\":512")) || bytes.Contains(body, []byte("\"response_format\"")) {
				t.Fatalf("final request was not the minimal compatibility shape: %s", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"{\\\"decisions\\\":[{\\\"candidate_id\\\":\\\"a\\\",\\\"verdict\\\":\\\"yes\\\"}]}\"}}]}"))
		}
	}))
	defer model.Close()
	task := Task{ID: "shape-retry", Agent: &AgentData{Model: "fixture", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL, "OPENAI_API_KEY": "fixture"}}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{
		"name": llm2jevMCPToolName,
		"arguments": map[string]any{
			"question":   "q",
			"candidates": []map[string]string{{"id": "a", "content": "x"}},
		},
	})
	if result["isError"] == true || attempts != 4 {
		t.Fatalf("result=%v attempts=%d", result, attempts)
	}
}

func TestLLM2JevMCPDoesNotRetryServerErrors(t *testing.T) {
	attempts := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}))
	defer model.Close()
	task := Task{ID: "no-retry", Agent: &AgentData{Model: "fixture", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL, "OPENAI_API_KEY": "fixture"}}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{
		"name": llm2jevMCPToolName,
		"arguments": map[string]any{
			"question":   "q",
			"candidates": []map[string]string{{"id": "a", "content": "x"}},
		},
	})
	if result["isError"] != true || attempts != 1 {
		t.Fatalf("result=%v attempts=%d", result, attempts)
	}
}

func TestLLM2JevMCPLogsSafeRequestMetadata(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"{\\\"decisions\\\":[{\\\"candidate_id\\\":\\\"a\\\",\\\"verdict\\\":\\\"yes\\\"}]}\"}}]}"))
	}))
	defer model.Close()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	secret := "fixture-secret-key"
	task := Task{ID: "safe-logs", Agent: &AgentData{Model: "fixture-model", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL, "OPENAI_API_KEY": secret}}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{
		"name": llm2jevMCPToolName,
		"arguments": map[string]any{
			"question":   "q",
			"candidates": []map[string]string{{"id": "a", "content": "x"}},
		},
	})
	if result["isError"] == true {
		t.Fatalf("decision failed: %v", result)
	}
	logText := logs.String()
	if !strings.Contains(logText, "result_class=success") || !strings.Contains(logText, "variant=max_tokens_json") {
		t.Fatalf("missing safe request metadata: %s", logText)
	}
	if strings.Contains(logText, secret) || strings.Contains(logText, model.URL) {
		t.Fatalf("sensitive endpoint data reached logs: %s", logText)
	}
}

func TestLLM2JevMCPRejectsInvalidVerdict(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"decisions\":[{\"candidate_id\":\"a\",\"verdict\":\"maybe\"}]}"}}]}`))
	}))
	defer model.Close()
	task := Task{ID: "invalid-decision-task", Agent: &AgentData{Model: "fixture-model", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL, "OPENAI_API_KEY": "fixture"}}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{
		"name": llm2jevMCPToolName,
		"arguments": map[string]any{
			"question":   "Is it valid?",
			"candidates": []map[string]string{{"id": "a", "content": "candidate"}},
		},
	})
	if result["isError"] != true {
		t.Fatalf("invalid verdict was accepted: %v", result)
	}
	if !strings.Contains(result["content"].([]any)[0].(map[string]any)["text"].(string), "invalid decision") {
		t.Fatalf("unexpected error: %v", result)
	}
}

func TestLLM2JevMCPCapabilitiesAndMeta(t *testing.T) {
	task := Task{ID: "capabilities", Agent: &AgentData{Model: "fixture", CustomEnv: map[string]string{"OPENAI_BASE_URL": "http://127.0.0.1:1234/v1"}}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{
		"name":      llm2jevMCPCapabilityTool,
		"arguments": map[string]any{},
		"_meta":     map[string]any{"progressToken": "capability"},
	})
	if result["isError"] == true {
		t.Fatalf("capabilities call failed: %v", result)
	}
	var capability map[string]any
	if err := json.Unmarshal([]byte(result["content"].([]any)[0].(map[string]any)["text"].(string)), &capability); err != nil {
		t.Fatal(err)
	}
	if capability["mode"] != "semantic_runtime" || capability["supports_logprobs"] != false {
		t.Fatalf("unexpected capabilities: %v", capability)
	}
}

func TestLLM2JevMCPRejectsBoundViolations(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("invalid decision input reached model")
	}))
	defer model.Close()
	task := Task{ID: "bounds", Agent: &AgentData{Model: "fixture", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL, "OPENAI_API_KEY": "fixture"}}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	for _, arguments := range []map[string]any{
		{"question": "q", "candidates": []map[string]string{{"id": "a", "content": "x"}, {"id": "a", "content": "y"}}},
		{"question": strings.Repeat("q", 16001), "candidates": []map[string]string{{"id": "a", "content": "x"}}},
		{"question": "q", "candidates": []map[string]string{{"id": "", "content": "x"}}},
	} {
		result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{"name": llm2jevMCPToolName, "arguments": arguments})
		if result["isError"] != true {
			t.Fatalf("invalid arguments were accepted: %v", result)
		}
	}
}

func TestProviderSupportsLLM2JevMCPMatrix(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "hermes", "qoder", "mcode"} {
		if !providerSupportsLLM2JevMCP(provider) {
			t.Fatalf("provider %q should support the managed decision MCP", provider)
		}
	}
	for _, provider := range []string{"codebuddy", "codearts", "cursor", "grok", "kimi", "dsh", "kiro", "qoderclicn", "qwen", "qwenpaw", "traecli", "dim", "omp", "opencode", "zeroclaw", "pi", "copilot", "antigravity", "deveco"} {
		if providerSupportsLLM2JevMCP(provider) {
			t.Fatalf("provider %q must not advertise managed decision MCP", provider)
		}
	}
}

func TestLLM2JevMCPIsOptionalWithoutAgentRuntimeCredentials(t *testing.T) {
	for _, task := range []Task{
		{ID: "no-agent"},
		{ID: "no-model", Agent: &AgentData{CustomEnv: map[string]string{"OPENAI_BASE_URL": "http://127.0.0.1"}}},
	} {
		config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, nil)
		if err != nil || len(config) != 0 || set != nil {
			t.Fatalf("task %+v unexpectedly enabled decision MCP: config=%s set=%v err=%v", task, config, set, err)
		}
	}
}

func TestLLM2JevMCPAllowsKeylessLoopbackRuntime(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("keyless request sent authorization header %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"{\\\"decisions\\\":[{\\\"candidate_id\\\":\\\"a\\\",\\\"verdict\\\":\\\"yes\\\"}]}\"}}]}"))
	}))
	defer model.Close()
	task := Task{ID: "keyless", Agent: &AgentData{Model: "fixture", CustomEnv: map[string]string{"OPENAI_BASE_URL": model.URL}}}
	config, set, err := startTaskLLM2JevMCP(context.Background(), task.ID, "claude", task, nil)
	if err != nil || len(config) == 0 || set == nil {
		t.Fatalf("keyless loopback runtime was not accepted: config=%s set=%v err=%v", config, set, err)
	}
	result := callLLM2JevMCP(t, llm2jevMCPURL(t, config), "tools/call", map[string]any{
		"name": llm2jevMCPToolName,
		"arguments": map[string]any{
			"question":   "q",
			"candidates": []map[string]string{{"id": "a", "content": "x"}},
		},
	})
	if result["isError"] == true {
		t.Fatalf("keyless model request failed: %v", result)
	}
	set.Close()
}

func TestNormalizeLLM2JevEndpoint(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
		ok    bool
	}{
		{input: "https://example.test/v1", want: "https://example.test/v1/chat/completions", ok: true},
		{input: "https://example.test/v1/chat/completions", want: "https://example.test/v1/chat/completions", ok: true},
		{input: "http://127.0.0.1:1234/", want: "http://127.0.0.1:1234/chat/completions", ok: true},
		{input: "https://example.test/v1?api_key=secret", ok: false},
		{input: "https://user:pass@example.test/v1", ok: false},
		{input: "file:///tmp/model", ok: false},
		{input: "not a url", ok: false},
	} {
		got, err := normalizeLLM2JevEndpoint(test.input)
		if test.ok {
			if err != nil || got != test.want {
				t.Fatalf("normalize(%q) = %q, %v; want %q", test.input, got, err, test.want)
			}
		} else if err == nil {
			t.Fatalf("normalize(%q) unexpectedly succeeded", test.input)
		}
	}
}
