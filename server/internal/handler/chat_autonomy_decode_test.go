package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatAutonomyDistinguishesAbsentNullAndLimits(t *testing.T) {
	for _, tc := range []struct{ raw, mode string }{
		{`{"content":"test"}`, ""},
		{`{"content":"test","autonomy_policy":null}`, "normal"},
		{`{"content":"test","autonomy_policy":{"mode":"autonomous","max_token_count":123}}`, "autonomous"},
	} {
		var req SendChatMessageRequest
		if err := json.Unmarshal([]byte(tc.raw), &req); err != nil {
			t.Fatal(err)
		}
		if tc.mode == "" {
			if req.AutonomyPolicy != nil {
				t.Fatal("absent must inherit")
			}
			continue
		}
		if req.AutonomyPolicy == nil || req.AutonomyPolicy.Mode != tc.mode {
			t.Fatalf("got %+v", req.AutonomyPolicy)
		}
	}
}

func TestSendChatPersistsAutonomyOverrideForDaemon(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"inherit", nil, ""},
		{"normal", nil, "normal"},
		{"limited", map[string]any{"mode": "autonomous", "max_token_count": 25, "max_duration_seconds": 10, "max_cost_usd_ticks": 500}, "autonomous"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentID := createHandlerTestAgent(t, "autonomy-message", []byte("[]"))
			sessionID := createHandlerTestChatSession(t, agentID)
			body := map[string]any{"content": "policy persistence fixture"}
			if tc.name != "inherit" {
				body["autonomy_policy"] = tc.value
			}
			req := withChatTestWorkspaceCtx(t, withURLParam(newRequest(http.MethodPost, "/api/chat/sessions/"+sessionID+"/messages", body), "sessionId", sessionID))
			recorder := httptest.NewRecorder()
			testHandler.SendChatMessage(recorder, req)
			if recorder.Code != http.StatusCreated {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var response SendChatMessageResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			task, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(response.TaskID))
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				Policy *struct {
					Mode   string `json:"mode"`
					Tokens int64  `json:"max_token_count"`
					Cost   int64  `json:"max_cost_usd_ticks"`
				} `json:"autonomy_policy"`
			}
			if len(task.Context) > 0 {
				if err := json.Unmarshal(task.Context, &envelope); err != nil {
					t.Fatal(err)
				}
			}
			if tc.want == "" {
				if envelope.Policy != nil {
					t.Fatal("absent policy should inherit")
				}
				return
			}
			if envelope.Policy == nil || envelope.Policy.Mode != tc.want {
				t.Fatalf("context=%s", task.Context)
			}
			if tc.name == "limited" && (envelope.Policy.Tokens != 25 || envelope.Policy.Cost != 500) {
				t.Fatalf("context=%s", task.Context)
			}
		})
	}
}
