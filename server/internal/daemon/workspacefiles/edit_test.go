package workspacefiles

import (
	"errors"
	"testing"
)

func TestAdmittedEditReplayDoesNotChangeALaterFileGeneration(t *testing.T) {
	scope, _ := fixture(t)
	if err := scope.Working.WriteFile("notes.md", []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := EditRequest{CommandID: "edit-once", OperationID: "admission", FileID: "file-id", Action: "merge_edit", Path: "notes.md", Base: "original", New: "submitted"}
	first, err := ApplyEdit(t.Context(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Working.WriteFile("notes.md", []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}

	replayed, err := ApplyEdit(t.Context(), scope, request)
	if err != nil || replayed != first {
		t.Fatalf("original receipt was not retained: %+v, %v", replayed, err)
	}
	content, err := scope.Working.ReadFile("notes.md")
	if err != nil || string(content) != "replacement" {
		t.Fatalf("replayed edit touched a later file: %q, %v", content, err)
	}
	request.OperationID = "another-admission"
	if _, err := ApplyEdit(t.Context(), scope, request); !errors.Is(err, ErrOperationMismatch) {
		t.Fatalf("older receipt attached to a new admission: %v", err)
	}
}

func TestAdmittedEditFailureCannotExecuteAfterTheFileAppears(t *testing.T) {
	scope, _ := fixture(t)
	request := EditRequest{CommandID: "edit-missing", OperationID: "admission", FileID: "file-id", Action: "merge_edit", Path: "notes.md", Base: "original", New: "submitted"}
	if _, err := ApplyEdit(t.Context(), scope, request); !errors.Is(err, ErrOperationRejected) {
		t.Fatal(err)
	}
	if err := scope.Working.WriteFile("notes.md", []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ApplyEdit(t.Context(), scope, request); !errors.Is(err, ErrOperationRejected) {
		t.Fatalf("failed edit executed after its refusal: %v", err)
	}
	content, err := scope.Working.ReadFile("notes.md")
	if err != nil || string(content) != "original" {
		t.Fatalf("refused edit changed the new file: %q, %v", content, err)
	}
}
