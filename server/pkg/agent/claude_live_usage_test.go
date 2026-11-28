package agent

import (
	"encoding/json"
	"testing"
)

func TestClaudeTurnPublishesUsageBeforeResult(t *testing.T) {
	messages := make(chan Message, 8)
	var observed TokenUsage
	turn := &claudeTurn{
		msgCh:        messages,
		coalescer:    newClaudeStreamCoalescer(messages, nil),
		usage:        make(map[string]TokenUsage),
		seenUsage:    make(map[string]struct{}),
		publishUsage: func(usage map[string]TokenUsage) { observed = totalTokenUsage(usage) },
	}
	for _, event := range []string{
		`{"type":"message_start","message":{"id":"message-1","model":"model","usage":{"input_tokens":9}}}`,
		`{"type":"message_delta","usage":{"output_tokens":4}}`,
		`{"type":"message_delta","usage":{"output_tokens":4}}`,
	} {
		turn.handleMessage(claudeSDKMessage{Type: "stream_event", Event: json.RawMessage(event)})
	}
	if observed != (TokenUsage{InputTokens: 9, OutputTokens: 4}) {
		t.Fatalf("live snapshot = %+v, want 9 input and 4 output before result", observed)
	}
}
