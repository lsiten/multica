package handler

import (
	"encoding/json"
	"errors"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// The runtime supplies filesystem support; the server restricts it to the
// current member's write authority before exposing controls in another client.
func restrictWorkspaceFileOperations(result *protocol.LocalReviewResult) error {
	var page map[string]json.RawMessage
	if err := json.Unmarshal(result.Page, &page); err != nil {
		return err
	}
	if page == nil {
		return errors.New("workspace directory page is missing")
	}
	page["file_operations_supported"] = json.RawMessage("false")
	page["file_operation_identity_supported"] = json.RawMessage("false")
	page["file_edit_identity_supported"] = json.RawMessage("false")
	encoded, err := json.Marshal(page)
	if err != nil {
		return err
	}
	result.Page = encoded
	return nil
}
