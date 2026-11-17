package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRemoteWorkspaceFileOperationsUseOwnedDirectoryWithoutGit(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.environmentServiceOwner = true
	d.localPathLocks = NewLocalPathLocker()
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	path := filepath.Join(root, "files")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	apply := func(id string, operation protocol.WorkspaceFileOperation) protocol.WorkspaceFileOperationResult {
		t.Helper()
		admission := "server-admission-" + id
		result := d.runRemoteReview(context.Background(), protocol.LocalReviewCommand{ID: id, FileOperationID: admission, WorkspaceID: "ws1", RuntimeID: "runtime", TaskID: "task1", ActorID: "member", Path: path, Action: "file_operation", FileOperation: &operation})
		if result.Error != "" {
			t.Fatalf("%s: %s", operation.Kind, result.Error)
		}
		var receipt protocol.WorkspaceFileOperationResult
		if err := json.Unmarshal(result.Page, &receipt); err != nil {
			t.Fatalf("%s receipt: %v", operation.Kind, err)
		}
		if receipt.OperationID != admission {
			t.Fatalf("%s lost the server metadata admission: %+v", operation.Kind, receipt)
		}
		return receipt
	}
	apply("folder", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateDirectory, Path: "notes"})
	apply("create", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "notes/first.md", Content: "original body"})
	refused := d.runRemoteReview(context.Background(), protocol.LocalReviewCommand{ID: "collision", WorkspaceID: "ws1", RuntimeID: "runtime", TaskID: "task1", ActorID: "member", Path: path, Action: "file_operation", FileOperation: &protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "notes/first.md", Content: "overwrite"}})
	if refused.Error == "" || !refused.FileOperationRejected || refused.EffectsUnknown {
		t.Fatalf("collision did not confirm an unchanged filesystem: %+v", refused)
	}
	apply("rename", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "notes/first.md", Destination: "notes/renamed.md"})
	deleted := apply("delete", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceTrash, Path: "notes/renamed.md"})
	trash := d.runRemoteReview(context.Background(), protocol.LocalReviewCommand{ID: "list-trash", WorkspaceID: "ws1", RuntimeID: "runtime", TaskID: "task1", ActorID: "member", Path: path, Action: "workspace_trash", Limit: 100})
	var listing struct {
		Entries []struct {
			RecoveryID string `json:"recovery_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(trash.Page, &listing); err != nil || trash.Error != "" || len(listing.Entries) != 1 || listing.Entries[0].RecoveryID != deleted.RecoveryID {
		t.Fatalf("recoverable remote files: %+v, error: %s, decode: %v", listing, trash.Error, err)
	}
	if err := os.WriteFile(filepath.Join(path, "notes/renamed.md"), []byte("later body"), 0o600); err != nil {
		t.Fatal(err)
	}
	apply("delete", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceTrash, Path: "notes/renamed.md"})
	apply("restore", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceRestore, Path: "notes/restored.md", RecoveryID: deleted.RecoveryID})
	for name, want := range map[string]string{"notes/renamed.md": "later body", "notes/restored.md": "original body"} {
		body, err := os.ReadFile(filepath.Join(path, name))
		if err != nil || string(body) != want {
			t.Fatalf("%s body %q, error: %v", name, body, err)
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 1 || entries[0].Name() != "notes" {
		t.Fatalf("private operation data entered the working directory: %v, error: %v", entries, err)
	}
}

func TestWorkspaceFileOperationWaitsForSharedDirectoryWriter(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.environmentServiceOwner = true
	d.localPathLocks = NewLocalPathLocker()
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	path := filepath.Join(root, "files")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	// Production writers register a durable lease visible to service children.
	release, err := d.localPathLocks.trackShared(context.Background(), path, "other-writer", true)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	command := protocol.LocalReviewCommand{ID: "create-once", WorkspaceID: "ws1", RuntimeID: "runtime", TaskID: "task1", ActorID: "member", Path: path, Action: "file_operation", FileOperation: &protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "new.md", Content: "requested"}}
	result := d.runRemoteReview(context.Background(), command)
	if result.Error == "" {
		t.Fatal("file operation ignored an active shared writer")
	}
	if _, err := os.Stat(filepath.Join(path, "new.md")); !os.IsNotExist(err) {
		t.Fatalf("busy operation changed a file: %v", err)
	}
	release()
	result = d.runRemoteReview(context.Background(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	content, err := os.ReadFile(filepath.Join(path, "new.md"))
	if err != nil || string(content) != "requested" {
		t.Fatalf("released operation content %q, error: %v", content, err)
	}
}
