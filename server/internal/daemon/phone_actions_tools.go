package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"github.com/multica-ai/multica/server/internal/communications"
	"io"
	"net/http"
	"strings"
	"time"
)

func phoneActionToolDescriptors() []map[string]any {
	return []map[string]any{
		{"name": phoneStartCallToolName, "description": "Start a bounded outbound call; record=true enables provider recording for later transcription.", "inputSchema": map[string]any{"type": "object", "required": []string{"to", "message", "idempotency_key"}, "properties": map[string]any{"to": map[string]any{"type": "string", "description": "E.164 destination number"}, "message": map[string]any{"type": "string", "maxLength": 4000}, "record": map[string]any{"type": "boolean"}, "idempotency_key": map[string]any{"type": "string", "maxLength": 200}}}},
		{"name": phoneGetCallToolName, "description": "Get the provider status of an outbound call.", "inputSchema": map[string]any{"type": "object", "required": []string{"call_sid"}, "properties": map[string]any{"call_sid": map[string]any{"type": "string", "maxLength": 128}}}},
		{"name": phoneCancelCallToolName, "description": "Cancel an outbound call that is still cancellable.", "inputSchema": map[string]any{"type": "object", "required": []string{"call_sid"}, "properties": map[string]any{"call_sid": map[string]any{"type": "string", "maxLength": 128}}}},
		{"name": phoneTranscriptsToolName, "description": "List provider transcripts already generated for an outbound call.", "inputSchema": map[string]any{"type": "object", "required": []string{"call_sid"}, "properties": map[string]any{"call_sid": map[string]any{"type": "string", "maxLength": 128}}}},
	}
}

func (s *phoneActionsMCPServer) handleCall(w http.ResponseWriter, r *http.Request, request pluginHookMCPRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		writePluginHookMCPError(w, request.ID, -32602, "unknown or invalid phone action")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), phoneActionsMCPCallTimeout)
	defer cancel()
	var result any
	var err error
	switch params.Name {
	case phoneStartCallToolName:
		var input struct {
			To             string `json:"to"`
			Message        string `json:"message"`
			IdempotencyKey string `json:"idempotency_key"`
			Record         bool   `json:"record"`
		}
		if err = decodePhoneArguments(params.Arguments, &input); err == nil {
			if strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 200 || len([]rune(input.Message)) == 0 || len([]rune(input.Message)) > 4000 {
				err = communications.ErrInvalidRequest
			} else {
				var twiml strings.Builder
				twiml.WriteString("<Response><Say>")
				_ = xml.EscapeText(&twiml, []byte(input.Message))
				twiml.WriteString("</Say></Response>")
				result, err = s.provider.StartCall(ctx, communications.CallRequest{To: input.To, Message: input.Message, Twiml: twiml.String(), Record: input.Record, IdempotencyKey: s.taskID + ":" + strings.TrimSpace(input.IdempotencyKey)})
				if err == nil {
					if call, ok := result.(communications.Call); ok {
						s.rememberCall(call.SID)
					}
				}
			}
		}
	case phoneGetCallToolName:
		var input struct {
			CallSID string `json:"call_sid"`
		}
		if err = decodePhoneArguments(params.Arguments, &input); err == nil {
			if !s.ownsCall(input.CallSID) {
				err = communications.ErrCallNotFound
			} else {
				result, err = s.provider.GetCall(ctx, input.CallSID)
			}
		}
	case phoneCancelCallToolName:
		var input struct {
			CallSID string `json:"call_sid"`
		}
		if err = decodePhoneArguments(params.Arguments, &input); err == nil {
			if !s.ownsCall(input.CallSID) {
				err = communications.ErrCallNotFound
			} else {
				result, err = s.provider.CancelCall(ctx, input.CallSID)
			}
		}
	case phoneTranscriptsToolName:
		var input struct {
			CallSID string `json:"call_sid"`
		}
		if err = decodePhoneArguments(params.Arguments, &input); err == nil {
			if !s.ownsCall(input.CallSID) {
				err = communications.ErrCallNotFound
			} else {
				result, err = s.provider.ListTranscriptions(ctx, input.CallSID)
			}
		}
	default:
		writePluginHookMCPError(w, request.ID, -32602, "unknown phone action")
		return
	}
	if err != nil {
		if s.logger != nil {
			s.logger.Info("identity phone tool call failed", "task_id", s.taskID, "error", err)
		}
		writePluginHookMCPResult(w, request.ID, map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}})
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		writePluginHookMCPError(w, request.ID, -32603, "could not encode phone result")
		return
	}
	writePluginHookMCPResult(w, request.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": string(encoded)}}})
}

func (s *phoneActionsMCPServer) rememberCall(sid string) {
	if sid == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.callSIDs == nil {
		s.callSIDs = make(map[string]struct{})
	}
	s.callSIDs[sid] = struct{}{}
}

func (s *phoneActionsMCPServer) ownsCall(sid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.callSIDs[sid]
	return ok
}

func decodePhoneArguments(raw json.RawMessage, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return communications.ErrInvalidRequest
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return communications.ErrInvalidRequest
	}
	return nil
}

// Stop outstanding calls on task shutdown. The provider TimeLimit also bounds
// calls whose create response was lost before a SID could be recorded.
func (s *phoneActionsMCPServer) cancelOwnedCalls() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.callSIDs))
	for sid := range s.callSIDs {
		ids = append(ids, sid)
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, sid := range ids {
		if _, err := s.provider.CancelCall(ctx, sid); err != nil && s.logger != nil {
			s.logger.Warn("phone call cleanup could not be confirmed", "task_id", s.taskID)
		}
	}
}
