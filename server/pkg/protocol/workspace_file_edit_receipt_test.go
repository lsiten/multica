package protocol

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestWorkspaceFileEditReceiptRejectsMissingEvidence(t *testing.T) {
	command := LocalReviewCommand{CommandID: "save", FileOperationID: "admission", FileID: "file", RelativePath: "notes.md", Action: "merge_edit"}
	base := WorkspaceFileEditResult{CommandID: "save", OperationID: "admission", FileID: "file", FilePath: "notes.md", Merged: true, PreviousSHA256: strings.Repeat("a", 64), CurrentSHA256: strings.Repeat("b", 64), Bytes: 4}
	for _, field := range []string{"command_id", "operation_id", "file_id", "file_path", "merged", "conflict", "bytes"} {
		t.Run(field, func(t *testing.T) {
			payload, err := json.Marshal(base)
			if err != nil {
				t.Fatal(err)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(payload, &raw); err != nil {
				t.Fatal(err)
			}
			delete(raw, field)
			payload, err = json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := ParseWorkspaceFileEditReceipt(command, LocalReviewResult{Edit: payload}); !errors.Is(err, ErrInvalidWorkspaceFileReceipt) {
				t.Fatalf("missing %s acknowledged an edit: %v", field, err)
			}
		})
	}
}
