package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRemoteIdentityEditKeepsItsReceiptAfterAnotherFileReplacesThePath(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.environmentServiceOwner, d.localPathLocks = true, NewLocalPathLocker()
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	path := filepath.Join(root, "files")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "notes.md")
	if err := os.WriteFile(file, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := protocol.LocalReviewCommand{ID: "relay-1", CommandID: "save-once", FileOperationID: "admission", FileID: "file-id", WorkspaceID: "ws1", RuntimeID: "runtime", TaskID: "task1", ActorID: "member", Path: path, RelativePath: "notes.md", Action: "merge_edit", EditBase: "original", EditNew: "submitted"}
	first := d.runRemoteReview(t.Context(), command)
	if _, err := protocol.ParseWorkspaceFileEditReceipt(command, first); err != nil {
		t.Fatalf("identity edit returned no valid wire receipt: %+v, %v", first, err)
	}
	body, err := os.ReadFile(file)
	if err != nil || string(body) != "submitted" {
		t.Fatalf("first edit did not reach the actual file: %q, %v", body, err)
	}
	if err := os.WriteFile(file, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	command.ID = "relay-2"

	replay := d.runRemoteReview(t.Context(), command)

	if replay.Error != "" || string(replay.Edit) != string(first.Edit) {
		t.Fatalf("relay replay changed its result: %+v", replay)
	}
	body, err = os.ReadFile(file)
	if err != nil || string(body) != "replacement" {
		t.Fatalf("relay replay touched a replacement: %q, %v", body, err)
	}
}
