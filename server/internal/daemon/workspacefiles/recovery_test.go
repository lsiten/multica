package workspacefiles

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestInterruptedDeletionRemainsRecoverableWithoutReapplyingToANewFile(t *testing.T) {
	scope, working := fixture(t)
	if err := os.WriteFile(filepath.Join(working, "file.md"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := request("delete-once", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceTrash, Path: "file.md"})
	if _, err := Apply(context.Background(), scope, input); err != nil {
		t.Fatal(err)
	}
	// Reconstruct a process interruption after the move but before the completed
	// journal publication. This exercises the durable storage contract, not a hook.
	journal := filepath.Join(scope.Storage.Name(), recordsPath)
	entries, err := os.ReadDir(journal)
	if err != nil {
		t.Fatal(err)
	}
	var receiptPath string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			receiptPath = filepath.Join(journal, entry.Name())
			break
		}
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var receipt map[string]json.RawMessage
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt["state"] = json.RawMessage(`"prepared"`)
	data, err = json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(working, "file.md"), []byte("later"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), scope, input); !errors.Is(err, ErrOperationUnconfirmed) {
		t.Fatalf("unknown deletion was reapplied: %v", err)
	}
	page, err := ListTrash(context.Background(), scope, TrashPageRequest{Limit: 100})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].RecoveryID != "delete-once" {
		t.Fatalf("interrupted deletion not recoverable: %+v, error: %v", page, err)
	}
	if _, err := Apply(context.Background(), scope, request("restore", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceRestore, Path: "recovered.md", RecoveryID: "delete-once"})); err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]string{"file.md": "later", "recovered.md": "original"} {
		content, err := os.ReadFile(filepath.Join(working, name))
		if err != nil || string(content) != expected {
			t.Fatalf("%s content %q, error: %v", name, content, err)
		}
	}
}
