package agent

import "encoding/json"

type claudeStreamUsage struct {
	active   map[string]string
	messages map[string]claudeStreamMessageUsage
}

type claudeStreamMessageUsage struct {
	model  string
	output int64
}

// Partial events expose response output usage before a multi-tool turn ends.
// message_delta output is cumulative for its message, not an additive delta.
func (s *claudeStreamUsage) observe(msg claudeSDKMessage, usage map[string]TokenUsage, seen map[string]struct{}) {
	var event struct {
		Type    string `json:"type"`
		Message struct {
			ID    string       `json:"id"`
			Model string       `json:"model"`
			Usage *claudeUsage `json:"usage"`
		} `json:"message"`
		Usage *claudeUsage `json:"usage"`
	}
	if json.Unmarshal(msg.Event, &event) != nil {
		return
	}
	if s.active == nil {
		s.active = make(map[string]string)
		s.messages = make(map[string]claudeStreamMessageUsage)
	}
	switch event.Type {
	case "message_start":
		m := event.Message
		if m.ID == "" || m.Model == "" {
			return
		}
		s.active[msg.ParentToolUseID] = m.ID
		if _, exists := s.messages[m.ID]; !exists {
			s.messages[m.ID] = claudeStreamMessageUsage{model: m.Model}
		}
		if _, counted := seen[m.ID]; !counted && m.Usage != nil {
			seen[m.ID] = struct{}{}
			u := usage[m.Model]
			u = addTokenUsage(u, TokenUsage{InputTokens: m.Usage.InputTokens, CacheReadTokens: m.Usage.CacheReadInputTokens, CacheWriteTokens: m.Usage.CacheCreationInputTokens})
			usage[m.Model] = u
		}
	case "message_delta":
		id := s.active[msg.ParentToolUseID]
		m, exists := s.messages[id]
		if !exists || event.Usage == nil || event.Usage.OutputTokens <= m.output {
			return
		}
		u := usage[m.model]
		u = addTokenUsage(u, TokenUsage{OutputTokens: event.Usage.OutputTokens - m.output})
		usage[m.model] = u
		m.output = event.Usage.OutputTokens
		s.messages[id] = m
	case "message_stop":
		delete(s.active, msg.ParentToolUseID)
	}
}
