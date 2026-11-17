package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestFileMutationRefusesAStaleSelectionBeforeAdmission(t *testing.T) {
	for _, tc := range []struct {
		kind     protocol.WorkspaceFileOperationKind
		previous protocol.WorkspaceFileOperationKind
	}{
		{protocol.WorkspaceMove, protocol.WorkspaceMove},
		{protocol.WorkspaceTrash, protocol.WorkspaceMove},
		{protocol.WorkspaceMove, protocol.WorkspaceTrash},
		{protocol.WorkspaceTrash, protocol.WorkspaceTrash},
	} {
		t.Run(string(tc.previous)+" then "+string(tc.kind), func(t *testing.T) {
			directory, _ := workspaceFileMutationFixture(t)
			originalID := directory.binding.FileID
			previous := protocol.WorkspaceFileOperation{Kind: tc.previous, Path: "notes.md"}
			if tc.previous == protocol.WorkspaceMove {
				previous.Destination = "original.md"
			}
			finishFileMutation(t, admitFileMutation(t, directory, "remove-original", previous))
			finishFileMutation(t, admitFileMutation(t, directory, "replacement", protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "notes.md"}))
			operation := protocol.WorkspaceFileOperation{Kind: tc.kind, Path: "notes.md", ExpectedFileID: originalID}
			if tc.kind == protocol.WorkspaceMove {
				operation.Destination = "selected.md"
			}
			command := protocol.LocalReviewCommand{Action: "file_operation", CommandID: "stale-selection", ActorID: testUserID, FileOperation: &operation}

			_, err := testHandler.admitWorkspaceFileOperation(t.Context(), directory, command)

			if !errors.Is(err, errWorkspaceFileUnavailable) {
				t.Fatalf("stale selection admitted a replacement: %v", err)
			}
			command.TaskID, command.Path = directory.binding.TaskID, directory.root
			request := withChatTestWorkspaceCtx(t, newRequest(http.MethodPost, "/execute", command))
			var response struct {
				Code string `json:"code"`
			}
			testutil.Call(t, testHandler.ForwardLocalReview, request).Want(http.StatusConflict).JSON(&response)
			if response.Code != "workspace_file_unavailable" {
				t.Fatalf("stale selection did not reach the identity guard: %+v", response)
			}
			var pending int
			dbfx.QueryRow(t, "SELECT count(*) FROM workspace_file_operation WHERE workspace_id=$1 AND host_key=$2 AND command_id='stale-selection'", directory.workspaceID, directory.hostKey).Scan(&pending)
			if pending != 0 {
				t.Fatal("a refused selection left a mutation reservation")
			}
			original, err := testHandler.Queries.GetWorkspaceFileResourceByID(t.Context(), db.GetWorkspaceFileResourceByIDParams{WorkspaceID: directory.workspaceID, ID: parseUUID(originalID)})
			wantPath, wantState := "original.md", "active"
			if tc.previous == protocol.WorkspaceTrash {
				wantPath, wantState = "notes.md", "trashed"
			}
			if err != nil || original.RelativePath != wantPath || original.State != wantState {
				t.Fatalf("refused selection changed the original: %+v, %v", original, err)
			}
			var replacementID string
			dbfx.QueryRow(t, "SELECT id::text FROM workspace_file_resource WHERE workspace_id=$1 AND directory_key=$2 AND relative_path='notes.md' AND state='active'", directory.workspaceID, directory.key).Scan(&replacementID)
			operation.ExpectedFileID, command.CommandID = replacementID, "fresh-selection"
			admitted := admitFileMutation(t, directory, command.CommandID, operation)
			finishFileMutation(t, admitted)
			replacement, err := testHandler.Queries.GetWorkspaceFileResourceByID(t.Context(), db.GetWorkspaceFileResourceByIDParams{WorkspaceID: directory.workspaceID, ID: parseUUID(replacementID)})
			if err != nil || (tc.kind == protocol.WorkspaceMove && replacement.RelativePath != "selected.md") || (tc.kind == protocol.WorkspaceTrash && replacement.State != "trashed") {
				t.Fatalf("fresh selection could not operate on its own generation: %+v, %v", replacement, err)
			}
		})
	}
}
