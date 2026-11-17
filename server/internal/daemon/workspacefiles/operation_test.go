package workspacefiles

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func fixture(t *testing.T) (Scope, string) {
	t.Helper()
	path := t.TempDir()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
	})
	storage, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := storage.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return Scope{Working: root, Storage: storage}, path
}

func request(id string, operation protocol.WorkspaceFileOperation) Request {
	return Request{CommandID: id, ActorID: "member", Operation: operation}
}

func TestCreateAndMovePreserveContentsAndNeverOverwrite(t *testing.T) {
	root, path := fixture(t)
	ctx := context.Background()
	_, err := Apply(ctx, root, request("folder", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateDirectory, Path: "notes"}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(ctx, root, request("create", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "notes/first.md", Content: "original"}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(ctx, root, request("move", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceMove, Path: "notes/first.md", Destination: "second.md"}))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(path, "second.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "original" {
		t.Fatalf("unexpected result: %v", string(body))
	}
	_, err = os.Stat(filepath.Join(path, "notes/first.md"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = Apply(ctx, root, request("collision", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "second.md", Content: "overwrite"}))
	if !errors.Is(err, os.ErrExist) || !errors.Is(err, ErrOperationRejected) {
		t.Fatalf("unexpected error: %v", err)
	}
	body, err = os.ReadFile(filepath.Join(path, "second.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "original" {
		t.Fatalf("unexpected result: %v", string(body))
	}
}

func TestTrashAndRestoreRetainFilesAndRefuseAReplacement(t *testing.T) {
	root, path := fixture(t)
	if err := os.Mkdir(filepath.Join(path, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "folder/data.bin"), []byte{0, 1, 2, 255}, 0o640); err != nil {
		t.Fatal(err)
	}
	result, err := Apply(context.Background(), root, request("delete", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceTrash, Path: "folder"}))
	if err != nil {
		t.Fatal(err)
	}
	if result.RecoveryID != "delete" {
		t.Fatalf("unexpected result: %v", result.RecoveryID)
	}
	if err := os.Mkdir(filepath.Join(path, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = Apply(context.Background(), root, request("restore-collision", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceRestore, Path: "folder", RecoveryID: result.RecoveryID}))
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = Apply(context.Background(), root, request("restore", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceRestore, Path: "restored", RecoveryID: result.RecoveryID}))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(path, "restored/data.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte{0, 1, 2, 255}, body) {
		t.Fatalf("restored bytes: %v", body)
	}
}

func TestRepeatedOperationReturnsOriginalReceiptWithoutTouchingNewFiles(t *testing.T) {
	root, path := fixture(t)
	if err := os.WriteFile(filepath.Join(path, "file.md"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := request("delete-once", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceTrash, Path: "file.md"})
	first, err := Apply(context.Background(), root, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "file.md"), []byte("later"), 0o600); err != nil {
		t.Fatal(err)
	}
	input.ActorID = "same-owner-through-another-client"
	repeated, err := Apply(context.Background(), root, input)
	if err != nil {
		t.Fatal(err)
	}
	if first != repeated {
		t.Fatalf("repeated result changed: %+v", repeated)
	}
	body, err := os.ReadFile(filepath.Join(path, "file.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "later" {
		t.Fatalf("unexpected result: %v", string(body))
	}
	input.Operation.Path = "different.md"
	_, err = Apply(context.Background(), root, input)
	if !errors.Is(err, ErrOperationMismatch) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOperationsRejectProtectedPathsAndSymbolicLinks(t *testing.T) {
	root, path := fixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(path, "link")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../escape", ".", ".git/config", ".multica/config", "link/outside.md"} {
		t.Run(name, func(t *testing.T) {
			_, err := Apply(context.Background(), root, request(name, protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: name}))
			if err == nil {
				t.Fatal("expected a rejected operation")
			}
		})
	}
	_, err := os.Stat(filepath.Join(outside, "outside.md"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file was created: %v", err)
	}
}

func TestFailedOperationCanRetryAfterANameCollisionIsRemoved(t *testing.T) {
	root, path := fixture(t)
	if err := os.WriteFile(filepath.Join(path, "file.md"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := request("create-once", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "file.md", Content: "requested"})
	if _, err := Apply(context.Background(), root, input); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected collision, got: %v", err)
	}
	if err := os.Remove(filepath.Join(path, "file.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), root, input); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(path, "file.md"))
	if err != nil || string(content) != "requested" {
		t.Fatalf("retry content %q, error: %v", content, err)
	}
}
