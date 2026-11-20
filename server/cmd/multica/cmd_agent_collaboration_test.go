package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentCollaborationReadKeepsCursorAndRejectsMisroutedResponse(t *testing.T) {
	const runID = "11111111-1111-4111-8111-111111111111"
	for _, responseID := range []string{runID, "22222222-2222-4222-8222-222222222222"} {
		t.Run(responseID, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/tasks/"+runID+"/answers" || r.URL.Query().Get("cursor") != "8:4096" || r.URL.Query().Get("limit") != "2" {
					t.Errorf("wrong incremental answer request: %s", r.URL)
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"task_id": responseID, "run_status": "running", "messages": []string{}, "next_cursor": "9:0", "has_more": false}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			t.Setenv("MULTICA_SERVER_URL", server.URL)
			t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
			t.Setenv("MULTICA_TOKEN", "test-token")
			cmd := newAgentReadCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{runID, "--cursor", "8:4096", "--limit", "2"})
			err := cmd.Execute()
			if responseID != runID {
				if err == nil {
					t.Fatal("misrouted answer advanced the caller's cursor")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				NextCursor string `json:"next_cursor"`
				HasMore    bool   `json:"has_more"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.NextCursor != "9:0" || result.HasMore {
				t.Fatalf("answer continuation lost: %s; %v", out.String(), err)
			}
		})
	}
}

func TestAgentCollaborationDelegateCommandPreservesRequestKey(t *testing.T) {
	const issueID = "11111111-1111-4111-8111-111111111111"
	const agentID = "22222222-2222-4222-8222-222222222222"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/issues/"+issueID+"/collaboration/delegate" || r.Method != http.MethodPost {
			t.Fatalf("unexpected delegate request: %s %s", r.Method, r.URL)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["target_agent_id"] != agentID || body["client_request_id"] != "retry-1" || body["content"] != "Review" {
			t.Fatalf("delegate body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"operation_id": "op-1", "operation": "delegate", "status": "accepted"})
	}))
	defer server.Close()
	t.Setenv("MULTICA_SERVER_URL", server.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")
	cmd := newAgentDelegateCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{issueID, agentID, "--content", "Review", "--request-id", "retry-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"operation_id": "op-1"`)) {
		t.Fatalf("delegate response = %s", out.String())
	}
}
