package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestFileOperationForwardingRequiresAuthoritativeReceipts(t *testing.T) {
	runtime := dbfx.Runtime(t, "file refusal runtime", testutil.Cols{"runtime_mode": "local", "status": "online"})
	agent := dbfx.Agent(t, "file refusal agent", runtime)
	issue := dbfx.Issue(t, "File refusal")
	host, err := testHandler.Queries.GetAgentRuntime(t.Context(), parseUUID(runtime))
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Cleanup(t, "DELETE FROM workspace_file_host_state WHERE workspace_id=$1 AND host_key=$2", testWorkspaceID, newWorkspaceFileDirectory(host, DocumentCommentResource{}).hostKey)
	for index, tc := range []struct {
		name              string
		rejected, unknown bool
		wantCode          string
		page              string
		wantStatus        int
	}{
		{"known refusal", true, false, "workspace_file_operation_rejected", "", http.StatusConflict},
		{"unknown effects", true, true, "", "", http.StatusConflict},
		{"unclassified error", false, false, "", "", http.StatusConflict},
		{"matching receipt", false, false, "", `{"kind":"file_operation","command_id":"create-note","operation":"create_file","path":"note.md","directory":false}`, http.StatusOK},
		{"unadmitted receipt", false, false, "", `{"kind":"file_operation","command_id":"create-note","operation_id":"forged-admission","operation":"create_file","path":"note.md","directory":false}`, http.StatusBadGateway},
		{"unknown effects with completed page", false, true, "", `{"kind":"file_operation","command_id":"create-note","operation":"create_file","path":"note.md","directory":false}`, http.StatusBadGateway},
		{"no effects with completed page", true, false, "", `{"kind":"file_operation","command_id":"create-note","operation":"create_file","path":"note.md","directory":false}`, http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fmt.Sprintf("/fixture/files/%d", index)
			task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "work_dir": root, "status": "completed"})
			dbfx.Cleanup(t, "DELETE FROM workspace_file_operation WHERE workspace_id=$1 AND root_path=$2", testWorkspaceID, root)
			dbfx.Cleanup(t, "DELETE FROM workspace_file_resource WHERE workspace_id=$1 AND binding_ref->>'taskId'=$2", testWorkspaceID, task)
			request := withChatTestWorkspaceCtx(t, newRequest(http.MethodPost, "/execute", protocol.LocalReviewCommand{TaskID: task, RuntimeID: runtime, Path: root, Action: "file_operation", CommandID: "create-note", FileOperationID: "forged-admission", FileOperation: &protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "note.md"}}))
			ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
			defer cancel()
			request = request.WithContext(ctx)
			done := make(chan struct{})
			go func() {
				defer close(done)
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
					command := testHandler.localReviewRelay.claim(testWorkspaceID, runtime)
					if command == nil {
						continue
					}
					if command.Action == "workspace_files" {
						page, err := json.Marshal(map[string]any{"path": root, "entries": []string{}, "file_operation_identity_supported": true})
						if err != nil {
							return
						}
						testHandler.localReviewRelay.complete(testWorkspaceID, runtime, command.ID, protocol.LocalReviewResult{ClaimToken: command.ClaimToken, Page: page})
						continue
					}
					result := protocol.LocalReviewResult{ClaimToken: command.ClaimToken, Page: []byte(tc.page), FileOperationRejected: tc.rejected, EffectsUnknown: tc.unknown}
					if tc.page == "" {
						result.Error = "fixture refusal"
					} else if tc.name != "unadmitted receipt" {
						var page map[string]json.RawMessage
						if json.Unmarshal(result.Page, &page) != nil {
							return
						}
						encoded, encodeErr := json.Marshal(command.FileOperationID)
						if encodeErr != nil {
							return
						}
						page["operation_id"] = encoded
						result.Page, encodeErr = json.Marshal(page)
						if encodeErr != nil {
							return
						}
					}
					testHandler.localReviewRelay.complete(testWorkspaceID, runtime, command.ID, result)
					return
				}
			}()
			var response struct {
				Code string `json:"code"`
			}
			testutil.Call(t, testHandler.ForwardLocalReview, request).Want(tc.wantStatus).JSON(&response)
			<-done
			if response.Code != tc.wantCode {
				t.Fatalf("refusal code %q, want %q", response.Code, tc.wantCode)
			}
			if tc.name == "matching receipt" {
				replay := withChatTestWorkspaceCtx(t, newRequest(http.MethodPost, "/execute", protocol.LocalReviewCommand{TaskID: task, RuntimeID: runtime, Path: root, Action: "file_operation", CommandID: "create-note", FileOperation: &protocol.WorkspaceFileOperation{Kind: protocol.WorkspaceCreateFile, Path: "note.md"}}))
				replayContext, cancelReplay := context.WithTimeout(replay.Context(), 2*time.Second)
				defer cancelReplay()
				testutil.Call(t, testHandler.ForwardLocalReview, replay.WithContext(replayContext)).Want(http.StatusOK)
				if replayContext.Err() != nil {
					t.Fatal("completed replay waited for an offline runtime")
				}
			}
		})
	}
}
