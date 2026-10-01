package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func callIdentityActionsMCP(t *testing.T, server *identityActionsMCPServer, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, server.path, strings.NewReader(body))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	var response map[string]any
	if recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
		}
	}
	return recorder.Code, response
}

func TestStartTaskIdentityActionsMCPRequiresExplicitCapability(t *testing.T) {
	config, set, err := startTaskIdentityActionsMCP(context.Background(), "task-1", false, func(context.Context, string, string, string, string) error {
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("startTaskIdentityActionsMCP: %v", err)
	}
	if config != nil || set != nil {
		t.Fatal("identity action MCP started without an explicit capability")
	}
}

func TestIdentityActionsMCPListsAndInvokesEmailTool(t *testing.T) {
	var got struct {
		taskID, recipient, subject, body string
	}
	config, set, err := startTaskIdentityActionsMCP(context.Background(), "task-1", true, func(_ context.Context, taskID, recipient, subject, body string) error {
		got.taskID, got.recipient, got.subject, got.body = taskID, recipient, subject, body
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("startTaskIdentityActionsMCP: %v", err)
	}
	t.Cleanup(set.Close)
	if !strings.Contains(string(config), identityActionsMCPName) {
		t.Fatalf("MCP config = %s", config)
	}
	server := set.server.Handler.(*identityActionsMCPServer)
	_, listed := callIdentityActionsMCP(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	result, ok := listed["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list result = %#v", listed)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) != 1 || tools[0].(map[string]any)["name"] != identitySendEmailToolName {
		t.Fatalf("tools = %#v", result["tools"])
	}
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"multica_identity_send_email","arguments":{"recipient":"alice@example.test","subject":"hello","body":"plain text"}}}`
	_, response := callIdentityActionsMCP(t, server, call)
	if _, ok := response["error"]; ok {
		t.Fatalf("email tool returned protocol error: %#v", response)
	}
	if got.taskID != "task-1" || got.recipient != "alice@example.test" || got.subject != "hello" || got.body != "plain text" {
		t.Fatalf("invocation = %#v", got)
	}
}

func TestIdentityActionsMCPReturnsToolErrorForServerFailure(t *testing.T) {
	server := &identityActionsMCPServer{taskID: "task-1", path: "/secret", sendEmail: func(context.Context, string, string, string, string) error {
		return errors.New("server rejected email")
	}}
	_, response := callIdentityActionsMCP(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"multica_identity_send_email","arguments":{"recipient":"alice@example.test","subject":"hello","body":"body"}}}`)
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("response = %#v", response)
	}
}

func TestIdentityActionsMCPRefusesUnknownTool(t *testing.T) {
	server := &identityActionsMCPServer{taskID: "task-1", path: "/secret", sendEmail: func(context.Context, string, string, string, string) error {
		t.Fatal("unknown tool reached invoker")
		return nil
	}}
	_, response := callIdentityActionsMCP(t, server, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"other","arguments":{}}}`)
	if _, ok := response["error"]; !ok {
		t.Fatalf("unknown tool response = %#v", response)
	}
}
