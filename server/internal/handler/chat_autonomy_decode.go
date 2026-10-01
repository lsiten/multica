package handler

import (
	"encoding/json"
	"github.com/multica-ai/multica/server/internal/service"
)

// An absent field inherits saved limits; JSON null explicitly selects normal.
func (r *SendChatMessageRequest) UnmarshalJSON(data []byte) error {
	type plain SendChatMessageRequest
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if raw, exists := fields["autonomy_policy"]; exists && string(raw) == "null" {
		decoded.AutonomyPolicy = &service.ChatAutonomyPolicy{Mode: "normal"}
	}
	*r = SendChatMessageRequest(decoded)
	return nil
}
