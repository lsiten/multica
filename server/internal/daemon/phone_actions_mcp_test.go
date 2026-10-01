package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/communications"
)

type recordingCallProvider struct {
	request communications.CallRequest
}

func (p *recordingCallProvider) StartCall(_ context.Context, request communications.CallRequest) (communications.Call, error) {
	p.request = request
	return communications.Call{SID: "CA-test", Status: "queued", To: request.To}, nil
}
func (p *recordingCallProvider) GetCall(context.Context, string) (communications.Call, error) {
	return communications.Call{SID: "CA-test", Status: "in-progress"}, nil
}
func (p *recordingCallProvider) CancelCall(context.Context, string) (communications.Call, error) {
	return communications.Call{SID: "CA-test", Status: "canceled"}, nil
}
func (p *recordingCallProvider) ListTranscriptions(context.Context, string) ([]communications.Transcript, error) {
	return []communications.Transcript{{SID: "TR-test", Text: "hello"}}, nil
}

func TestPhoneActionsMCPBuildsBoundedTwiml(t *testing.T) {
	provider := &recordingCallProvider{}
	server := &phoneActionsMCPServer{taskID: "task-1", path: "/token", provider: provider}
	request := pluginHookMCPRequest{ID: json.RawMessage("1"), Method: "tools/call"}
	request.Params, _ = json.Marshal(map[string]any{
		"name": phoneStartCallToolName,
		"arguments": map[string]any{
			"to": "+15551112222", "message": "<hello & goodbye>", "idempotency_key": "call-1",
		},
	})
	response := httptest.NewRecorder()
	server.handleCall(response, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/token", nil), request)
	if response.Code != http.StatusOK || provider.request.To != "+15551112222" {
		t.Fatalf("response=%d request=%+v body=%s", response.Code, provider.request, response.Body.String())
	}
	if provider.request.Twiml != "<Response><Say>&lt;hello &amp; goodbye&gt;</Say></Response>" {
		t.Fatalf("twiml=%q", provider.request.Twiml)
	}
	if provider.request.IdempotencyKey != "task-1:call-1" {
		t.Fatalf("idempotency key=%q", provider.request.IdempotencyKey)
	}
}

func TestPhoneActionsMCPRejectsUnknownTool(t *testing.T) {
	server := &phoneActionsMCPServer{path: "/token", provider: &recordingCallProvider{}}
	request := pluginHookMCPRequest{ID: json.RawMessage("1"), Method: "tools/call"}
	request.Params, _ = json.Marshal(map[string]any{"name": "unknown"})
	response := httptest.NewRecorder()
	server.handleCall(response, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/token", nil), request)
	if response.Code != http.StatusOK || response.Body.Len() == 0 {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPhoneActionsRefuseMissingKeyAndForeignCall(t *testing.T) {
	provider := &recordingCallProvider{}
	server := &phoneActionsMCPServer{taskID: "task", path: "/token", provider: provider}
	for _, arguments := range []string{
		`{"name":"multica_identity_start_call","arguments":{"to":"+15551112222","message":"hello"}}`,
		`{"name":"multica_identity_cancel_call","arguments":{"call_sid":"CA-foreign"}}`,
	} {
		recorder := httptest.NewRecorder()
		server.handleCall(recorder, httptest.NewRequest(http.MethodPost, "/", nil), pluginHookMCPRequest{ID: json.RawMessage("1"), Params: json.RawMessage(arguments)})
		var output struct {
			Result struct {
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &output); err != nil {
			t.Fatal(err)
		}
		if !output.Result.IsError {
			t.Fatalf("not rejected: %s", recorder.Body.String())
		}
	}
	if provider.request.To != "" {
		t.Fatal("invalid input initiated phone call")
	}
}
