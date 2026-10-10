package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestCodexFileApprovalRetainsProviderReportedScope(t *testing.T) {
	c, _, _ := newTestCodexClient(t)
	c.approvalContext = context.Background()
	c.notificationProtocol = "raw"
	received := make(chan ApprovalRequest, 1)
	c.cfg.RequestApproval = func(_ context.Context, request ApprovalRequest) (bool, error) { received <- request; return false, nil }
	c.handleLine(`{"method":"item/started","params":{"item":{"id":"file-op","type":"fileChange","changes":[{"path":"old.ts","kind":{"type":"delete"},"diff":"old content"},{"path":"a.ts","kind":{"type":"update","move_path":"b.ts"},"diff":"patch"}]}}}`)
	c.handleLine(`{"id":22,"method":"item/fileChange/requestApproval","params":{"itemId":"file-op","reason":"Remove obsolete code"}}`)
	select {
	case request := <-received:
		if len(request.FileChanges) != 2 || request.FileChanges[0].Path != "old.ts" || request.FileChanges[0].Kind != "delete" || request.FileChanges[1].MovePath != "b.ts" {
			t.Fatalf("lost file operation scope: %+v", request.FileChanges)
		}
		if string(request.Params) != `{"itemId":"file-op","reason":"Remove obsolete code"}` {
			t.Fatal("rewrote the provider's decision parameters")
		}
		c.handleLine(`{"method":"item/completed","params":{"item":{"id":"file-op","type":"fileChange","status":"declined","changes":[]}}}`)
		c.mu.Lock()
		retained := len(c.approvalFiles)
		c.mu.Unlock()
		if retained != 0 {
			t.Fatal("completed item retained stale review scope")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("file approval was not routed to its local reviewer")
	}
}

func TestCodexNativeQuestionNamesThePlatformDeliveryRoute(t *testing.T) {
	for _, request := range []string{
		`{"id":23,"method":"item/tool/requestUserInput","params":{"questions":[]}}`,
		`{"id":23,"method":"mcpServer/elicitation/request","params":{"serverName":"external","mode":"form","message":"Choose a value","requestedSchema":{"type":"object","properties":{}}}}`,
		`{"id":23,"method":"mcpServer/elicitation/request","params":{"serverName":"external","mode":"form","message":"Enter a value","_meta":{"codex_approval_kind":"mcp_tool_call"},"requestedSchema":{"type":"object","properties":{"value":{"type":"string"}}}}}`,
	} {
		t.Run(request, func(t *testing.T) {
			c, writer, _ := newTestCodexClient(t)
			c.handleLine(request)
			lines := writer.Lines()
			if len(lines) != 1 {
				t.Fatal("native question received no routing reply")
			}
			var reply struct {
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
				Result json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal([]byte(lines[0]), &reply); err != nil {
				t.Fatal(err)
			}
			if reply.Error == nil || len(reply.Result) != 0 {
				t.Fatal("native question guessed a member response")
			}
		})
	}
}
