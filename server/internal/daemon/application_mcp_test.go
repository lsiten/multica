package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const applicationMCPAppID = "11111111-1111-4111-8111-111111111111"
const applicationMCPRuntimeID = "22222222-2222-4222-8222-222222222222"
const applicationMCPWorkspaceID = "33333333-3333-4333-8333-333333333333"
const applicationMCPTaskID = "44444444-4444-4444-8444-444444444444"

func applicationMCPFixture(t *testing.T, backend http.Handler) (*Daemon, Task, context.CancelFunc) {
	t.Helper()
	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	broker, err := startBuiltinMCPBroker(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); broker.close() })
	d := &Daemon{client: NewClient(server.URL), builtinMCP: broker}
	d.client.token = "owner-token-must-never-be-used"
	d.applicationServerCapabilities.Store(applicationMCPRuntimeID, true)
	task := Task{ID: applicationMCPTaskID, WorkspaceID: applicationMCPWorkspaceID, RuntimeID: applicationMCPRuntimeID, AgentID: applicationMCPAppID, AuthToken: "mat_fixture"}
	return d, task, cancel
}

func startApplicationMCPFixture(t *testing.T, d *Daemon, task Task, ctx context.Context) (string, *applicationMCPSet) {
	t.Helper()
	config, set, err := d.startTaskApplicationMCP(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if set == nil {
		t.Fatal("application tools were not registered")
	}
	t.Cleanup(set.Close)
	if strings.Contains(string(config), task.AuthToken) || strings.Contains(string(config), d.client.token) {
		t.Fatal("management credential reached provider MCP config")
	}
	endpoint := taskMCPReadinessEndpoint(config, applicationMCPName)
	if endpoint == "" {
		t.Fatalf("missing MCP endpoint: %s", config)
	}
	return endpoint, set
}

func callApplicationMCPFixture(t *testing.T, endpoint, name string, args any) map[string]any {
	t.Helper()
	request := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: 3 * time.Second}).Post(endpoint, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded struct {
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Result == nil {
		t.Fatal("tool call did not return a result")
	}
	return decoded.Result
}

func TestApplicationMCPUsesTaskCredentialAndRoutesAllManagementTools(t *testing.T) {
	for _, test := range []struct {
		name, method, path string
		args               map[string]any
	}{
		{"multica_application_catalog", "GET", "/api/applications", map[string]any{"action": "list"}},
		{"multica_application_catalog", "GET", "/api/applications/" + applicationMCPAppID, map[string]any{"action": "get", "application_id": applicationMCPAppID}},
		{"multica_application_catalog", "POST", "/api/applications", map[string]any{"action": "create", "body": map[string]any{"name": "composition", "kind": "composition", "project_id": applicationMCPAppID}}},
		{"multica_application_catalog", "PATCH", "/api/applications/" + applicationMCPAppID, map[string]any{"action": "update", "application_id": applicationMCPAppID, "body": map[string]any{"revision": 7, "name": "updated"}}},
		{"multica_application_catalog", "DELETE", "/api/applications/" + applicationMCPAppID, map[string]any{"action": "delete", "application_id": applicationMCPAppID, "revision": 7}},
		{"multica_application_catalog", "GET", "/api/applications/" + applicationMCPAppID + "/plan", map[string]any{"action": "plan", "application_id": applicationMCPAppID}},
		{"multica_application_control", "POST", "/api/applications/" + applicationMCPAppID + "/operations", map[string]any{"application_id": applicationMCPAppID, "body": map[string]any{"action": "start", "revision": 7, "runtime_id": applicationMCPRuntimeID, "idempotency_key": "same-operation"}}},
		{"multica_application_status", "GET", "/api/applications/board", map[string]any{}},
		{"multica_application_operations", "GET", "/api/applications/" + applicationMCPAppID + "/operations/" + applicationMCPTaskID, map[string]any{"application_id": applicationMCPAppID, "operation_id": applicationMCPTaskID}},
		{"multica_application_operations", "POST", "/api/applications/" + applicationMCPAppID + "/operations/" + applicationMCPTaskID + "/cancel", map[string]any{"application_id": applicationMCPAppID, "operation_id": applicationMCPTaskID, "cancel": true}},
		{"multica_application_logs", "GET", "/api/applications/" + applicationMCPAppID + "/instances/" + applicationMCPTaskID + "/logs", map[string]any{"application_id": applicationMCPAppID, "instance_id": applicationMCPTaskID, "cursor": "host:0:8", "limit": 4096}},
		{"multica_application_service_access", "POST", "/api/applications/" + applicationMCPAppID + "/endpoints/" + applicationMCPTaskID + "/service-access", map[string]any{"application_id": applicationMCPAppID, "endpoint_id": applicationMCPTaskID}},
	} {
		t.Run(test.name+test.method, func(t *testing.T) {
			var requests atomic.Int64
			d, task, _ := applicationMCPFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("request=%s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer mat_fixture" || r.Header.Get("X-Workspace-ID") != applicationMCPWorkspaceID || r.Header.Get("X-Task-ID") != applicationMCPTaskID {
					t.Errorf("task context was replaced: %v", r.Header)
				}
				if test.name == "multica_application_control" {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["idempotency_key"] != "same-operation" || body["runtime_id"] != applicationMCPRuntimeID {
						t.Errorf("operation identity changed: %v", body)
					}
				}
				if test.name == "multica_application_logs" && (r.URL.Query().Get("cursor") != "host:0:8" || r.URL.Query().Get("limit") != "4096") {
					t.Error("log cursor or limit changed")
				}
				if r.Method == http.MethodDelete {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if _, err := io.WriteString(w, `{"id":"`+applicationMCPAppID+`","state":"queued"}`); err != nil {
					t.Error(err)
				}
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			endpoint, _ := startApplicationMCPFixture(t, d, task, ctx)
			result := callApplicationMCPFixture(t, endpoint, test.name, test.args)
			if result["isError"] == true || requests.Load() != 1 {
				t.Fatalf("request did not complete: %v calls=%d", result, requests.Load())
			}
		})
	}
}

func TestApplicationMCPRejectsUnsafeArgumentsAndOwnerCredentialFallback(t *testing.T) {
	var requests atomic.Int64
	d, task, _ := applicationMCPFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", 500)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint, _ := startApplicationMCPFixture(t, d, task, ctx)
	for _, test := range []struct {
		name string
		args any
	}{
		{"multica_application_control", map[string]any{"application_id": "../settings", "body": map[string]any{}}},
		{"multica_application_control", map[string]any{"application_id": applicationMCPAppID, "body": []string{"invalid"}}},
		{"multica_application_status", map[string]any{"workspace_id": "other-workspace"}},
		{"multica_application_status", nil},
		{"multica_application_logs", map[string]any{"application_id": applicationMCPAppID, "instance_id": applicationMCPTaskID, "limit": 65537}},
		{"multica_application_catalog", map[string]any{"application_id": applicationMCPAppID, "action": "delete", "revision": 0}},
	} {
		if result := callApplicationMCPFixture(t, endpoint, test.name, test.args); result["isError"] != true {
			t.Fatalf("unsafe arguments accepted: %v", result)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid arguments reached the backend")
	}
	task.AuthToken = "owner-token"
	if _, set, err := d.startTaskApplicationMCP(ctx, task); err == nil || set != nil {
		t.Fatal("owner credential substituted for task credential")
	}
	d.applicationServerCapabilities.Delete(task.RuntimeID)
	if config, set, err := d.startTaskApplicationMCP(ctx, task); err != nil || set != nil || len(config) != 0 {
		t.Fatal("old backend received application tools")
	}
}

func TestApplicationMCPListsCompleteToolSchemasAndReturnsPermissionFailure(t *testing.T) {
	d, task, _ := applicationMCPFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"application access forbidden"}`, http.StatusForbidden)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint, _ := startApplicationMCPFixture(t, d, task, ctx)
	response, err := http.Post(endpoint, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var list struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Type                 string `json:"type"`
					AdditionalProperties bool   `json:"additionalProperties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Result.Tools) != 6 {
		t.Fatalf("missing application capabilities: %+v", list.Result.Tools)
	}
	for _, tool := range list.Result.Tools {
		if tool.InputSchema.Type != "object" || tool.InputSchema.AdditionalProperties {
			t.Fatalf("tool arguments are not bounded: %+v", tool)
		}
	}
	result := callApplicationMCPFixture(t, endpoint, "multica_application_status", map[string]any{})
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if result["isError"] != true || !strings.Contains(string(encoded), "application access forbidden") {
		t.Fatalf("permission failure was hidden: %s", encoded)
	}
}

func TestApplicationMCPCancellationRevokesRouteAndCancelsInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	upstreamCancelled := make(chan struct{})
	d, task, _ := applicationMCPFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(upstreamCancelled)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint, _ := startApplicationMCPFixture(t, d, task, ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		callApplicationMCPFixture(t, endpoint, "multica_application_status", map[string]any{})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not reach backend")
	}
	cancel()
	select {
	case <-upstreamCancelled:
	case <-time.After(time.Second):
		t.Fatal("task cancellation did not cancel the API request")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("application tool did not finish after task cancellation")
	}
	deadline := time.Now().Add(time.Second)
	for {
		response, err := http.Post(endpoint, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task MCP route remained usable")
		}
		time.Sleep(time.Millisecond)
	}
}
