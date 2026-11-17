package workspacefiles

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAdmittedFailureReplaysWithoutReapplyingAfterTheFilesystemChanges(t *testing.T) {
	scope, working := fixture(t)
	name := filepath.Join(working, "file.md")
	if err := os.WriteFile(name, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := request("once", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "file.md", Content: "requested"})
	input.OperationID = "server-admission"
	if _, err := Apply(t.Context(), scope, input); !errors.Is(err, ErrOperationRejected) {
		t.Fatalf("expected refusal: %v", err)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}

	_, err := Apply(t.Context(), scope, input)

	if !errors.Is(err, ErrOperationRejected) {
		t.Fatalf("known refusal changed on retry: %v", err)
	}
	if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected admission executed again: %v", err)
	}
}
