package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestCodexApprovalPresentationPreservesExactArguments(t *testing.T) {
	params := json.RawMessage(`{"command":["printf","line\nnext","a b"],"cwd":"/tmp/test","reason":"Check the output"}`)
	op := codexApprovalOperation(agent.ApprovalRequest{Method: "execCommandApproval", Params: params})
	if op.Kind != "command" || op.Target != `"printf" "line\nnext" "a b"` || op.Location != "/tmp/test" || op.Details != string(params) {
		t.Fatalf("lost the operation's exact argument boundaries: %+v", op)
	}
}

func TestCodexApprovalPresentationUsesProviderFileScope(t *testing.T) {
	op := codexApprovalOperation(agent.ApprovalRequest{Method: "item/fileChange/requestApproval", Params: json.RawMessage(`{"itemId":"file-op"}`), FileChanges: []agent.ApprovalFileChange{{Path: "old.ts", Kind: "delete"}, {Path: "new.ts", Kind: "add"}}})
	if op.Kind != "files" || len(op.Files) != 2 || op.Files[0].Kind != "delete" || op.Files[0].Path != "old.ts" || strings.Contains(op.Target, "file-op") {
		t.Fatalf("operation ID replaced the member's file scope: %+v", op)
	}
}
