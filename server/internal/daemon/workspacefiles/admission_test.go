package workspacefiles

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestCompletedFileOperationCannotAdoptAnotherMetadataAdmission(t *testing.T) {
	for _, originalAdmission := range []string{"", "original-admission"} {
		t.Run("original="+originalAdmission, func(t *testing.T) {
			scope, working := fixture(t)
			if err := os.WriteFile(filepath.Join(working, "file.md"), []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			input := request("delete-once", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceTrash, Path: "file.md"})
			input.OperationID = originalAdmission
			if _, err := Apply(t.Context(), scope, input); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(working, "file.md"), []byte("new generation"), 0o600); err != nil {
				t.Fatal(err)
			}
			input.OperationID = "new-metadata-admission"

			_, err := Apply(t.Context(), scope, input)

			if !errors.Is(err, ErrOperationMismatch) {
				t.Fatalf("old physical receipt was assigned to a new metadata admission: %v", err)
			}
			body, err := os.ReadFile(filepath.Join(working, "file.md"))
			if err != nil || string(body) != "new generation" {
				t.Fatalf("replacement changed: %q, error: %v", body, err)
			}
		})
	}
}

func TestFileOperationReplayRetainsItsMetadataAdmission(t *testing.T) {
	scope, working := fixture(t)
	input := request("create-once", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "file.md", Content: "original"})
	input.OperationID = "metadata-admission"
	if _, err := Apply(t.Context(), scope, input); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(working, "file.md"), []byte("later edits"), 0o600); err != nil {
		t.Fatal(err)
	}
	input.ActorID = "same-owner-through-another-provider"

	result, err := Apply(t.Context(), scope, input)

	if err != nil || result.OperationID != input.OperationID {
		t.Fatalf("metadata admission was lost on replay: %+v, error: %v", result, err)
	}
	body, err := os.ReadFile(filepath.Join(working, "file.md"))
	if err != nil || string(body) != "later edits" {
		t.Fatalf("replay changed later edits: %q, error: %v", body, err)
	}
}
